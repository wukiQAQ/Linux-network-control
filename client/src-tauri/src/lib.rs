// MeTD Rust 侧：持有连接配置、托盘与自启能力，通过 reqwest 访问远程 netmon API。
use reqwest::header::AUTHORIZATION;
use serde_json::Value;
use std::fs::OpenOptions;
use std::io::Write;
use std::sync::Mutex;
use std::time::{Instant, SystemTime, UNIX_EPOCH};
use tauri::menu::{Menu, MenuItem};
use tauri::tray::{MouseButton, MouseButtonState, TrayIconBuilder, TrayIconEvent};
use tauri::{Manager, State};

#[derive(Clone)]
struct Conn {
    base: String,
    token: String,
}

struct ApiState {
    client: reqwest::Client,
    conn: Mutex<Option<Conn>>,
}

fn log_line(msg: &str) {
    let ts = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_secs())
        .unwrap_or(0);
    let path = std::env::temp_dir().join("metd.log");
    if let Ok(mut f) = OpenOptions::new().create(true).append(true).open(path) {
        let _ = writeln!(f, "[{ts}] {msg}");
    }
}

#[tauri::command]
fn ping() -> Result<String, String> {
    log_line("ping from frontend");
    Ok("pong".to_string())
}

#[tauri::command]
fn set_connection(state: State<'_, ApiState>, base: String, token: String) -> Result<(), String> {
    log_line(&format!("set_connection base={base} token_set={}", !token.is_empty()));
    let mut guard = state
        .conn
        .lock()
        .map_err(|_| "连接状态锁获取失败".to_string())?;
    *guard = Some(Conn { base, token });
    log_line("set_connection ok");
    Ok(())
}

#[tauri::command]
fn clear_connection(state: State<'_, ApiState>) -> Result<(), String> {
    let mut guard = state
        .conn
        .lock()
        .map_err(|_| "连接状态锁获取失败".to_string())?;
    *guard = None;
    log_line("clear_connection ok");
    Ok(())
}

// 统一的远程请求实现：api_get / api_post 共用（POST 不带请求体）。
async fn request_json(state: &ApiState, path: String, post: bool) -> Result<Value, String> {
    let started = Instant::now();
    let method = if post { "POST" } else { "GET" };
    log_line(&format!("{method} start path={path}"));
    let conn = {
        let guard = state
            .conn
            .lock()
            .map_err(|_| "连接状态锁获取失败".to_string())?;
        guard.clone()
    };
    let conn = match conn {
        Some(c) => c,
        None => {
            log_line("request err: 尚未配置服务器连接");
            return Err("尚未配置服务器连接".to_string());
        }
    };

    let full = format!(
        "{}{}",
        conn.base.trim_end_matches('/'),
        if path.starts_with('/') {
            path.as_str()
        } else {
            "/api/v1/traffic/now"
        }
    );
    let mut req = if post {
        state.client.post(&full)
    } else {
        state.client.get(&full)
    };
    if !conn.token.is_empty() {
        req = req.header(AUTHORIZATION, format!("Bearer {}", conn.token));
    }
    match req.send().await {
        Ok(resp) => {
            let status = resp.status();
            let body = resp.text().await.unwrap_or_default();
            log_line(&format!(
                "{method} done path={path} status={} elapsed_ms={}",
                status.as_u16(),
                started.elapsed().as_millis()
            ));
            if !status.is_success() {
                return Err(format!("HTTP {}: {}", status.as_u16(), friendly_body(&body)));
            }
            serde_json::from_str(&body).map_err(|e| {
                log_line(&format!("{method} parse err: {e}"));
                format!("响应解析失败: {e}")
            })
        }
        Err(e) => {
            log_line(&format!(
                "{method} err path={path} elapsed_ms={} err={e}",
                started.elapsed().as_millis()
            ));
            Err(format!("请求失败: {e}"))
        }
    }
}

#[tauri::command]
async fn api_get(state: State<'_, ApiState>, path: String) -> Result<Value, String> {
    request_json(&state, path, false).await
}

// POST 命令（无请求体）：用于开始抓包等动作类接口。
#[tauri::command]
async fn api_post(state: State<'_, ApiState>, path: String) -> Result<Value, String> {
    request_json(&state, path, true).await
}

// 把服务端的抓包文件下载到本机临时目录（%TEMP%\MeTD-captures），返回保存路径。
#[tauri::command]
async fn save_capture(state: State<'_, ApiState>, path: String) -> Result<String, String> {
    let conn = {
        let guard = state
            .conn
            .lock()
            .map_err(|_| "连接状态锁获取失败".to_string())?;
        guard.clone()
    };
    let conn = conn.ok_or_else(|| "尚未配置服务器连接".to_string())?;
    let url = format!(
        "{}{}",
        conn.base.trim_end_matches('/'),
        if path.starts_with('/') { path.as_str() } else { "" }
    );
    let mut req = state.client.get(&url);
    if !conn.token.is_empty() {
        req = req.header(AUTHORIZATION, format!("Bearer {}", conn.token));
    }
    let resp = req.send().await.map_err(|e| format!("下载失败: {e}"))?;
    if !resp.status().is_success() {
        return Err(format!("下载失败: HTTP {}", resp.status().as_u16()));
    }
    let bytes = resp.bytes().await.map_err(|e| format!("读取响应失败: {e}"))?;
    let dir = std::env::temp_dir().join("MeTD-captures");
    std::fs::create_dir_all(&dir).map_err(|e| format!("创建目录失败: {e}"))?;
    let stamp = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_secs())
        .unwrap_or(0);
    let dest = dir.join(format!("metd-capture-{stamp}.pcap"));
    std::fs::write(&dest, &bytes).map_err(|e| format!("保存文件失败: {e}"))?;
    log_line(&format!("save_capture {} bytes -> {}", bytes.len(), dest.display()));
    Ok(dest.to_string_lossy().to_string())
}

// 在注册表里查 Wireshark 安装路径（Windows 的标准登记位置，兼容装在 D 盘等情况）。
fn wireshark_from_registry() -> Option<String> {
    const KEYS: [&str; 3] = [
        r"HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\Wireshark.exe",
        r"HKLM\SOFTWARE\Wireshark",
        r"HKCU\SOFTWARE\Wireshark",
    ];
    for key in KEYS {
        let output = match std::process::Command::new("reg").args(["query", key, "/ve"]).output() {
            Ok(o) => o,
            Err(_) => continue,
        };
        if !output.status.success() {
            continue;
        }
        let text = String::from_utf8_lossy(&output.stdout);
        for token in text.split_whitespace() {
            let t = token.trim().trim_matches('"');
            let lower = t.to_lowercase();
            if lower.ends_with("wireshark.exe") && std::path::Path::new(t).exists() {
                return Some(t.to_string());
            }
            if lower.len() > 3 && lower.as_bytes()[1] == b':' && lower.as_bytes()[2] == b'\\' {
                let candidate = std::path::Path::new(t).join("Wireshark.exe");
                if candidate.exists() {
                    return Some(candidate.to_string_lossy().to_string());
                }
            }
        }
    }
    None
}

// 用 Wireshark 打开本地 pcap 文件；找不到 Wireshark 时退回系统默认程序。
#[tauri::command]
fn open_in_wireshark(path: String) -> Result<String, String> {
    if !std::path::Path::new(&path).exists() {
        return Err("抓包文件不存在".to_string());
    }
    let mut candidates: Vec<String> = Vec::new();
    if let Some(p) = wireshark_from_registry() {
        candidates.push(p);
    }
    candidates.push("wireshark.exe".to_string());
    candidates.push(r"C:\Program Files\Wireshark\Wireshark.exe".to_string());
    candidates.push(r"C:\Program Files (x86)\Wireshark\Wireshark.exe".to_string());
    for exe in candidates {
        if std::process::Command::new(&exe).arg(&path).spawn().is_ok() {
            log_line(&format!("opened in wireshark via {exe}: {path}"));
            return Ok(format!("已用 Wireshark 打开：{path}"));
        }
    }
    if std::process::Command::new("cmd")
        .args(["/C", "start", "", path.as_str()])
        .spawn()
        .is_ok()
    {
        log_line(&format!("wireshark not found, opened by default app: {path}"));
        return Ok(format!("未找到 Wireshark，已用系统默认程序打开：{path}"));
    }
    Err(format!("未找到 Wireshark，请手动打开文件：{path}"))
}

fn friendly_body(body: &str) -> String {
    if let Ok(v) = serde_json::from_str::<Value>(body) {
        if let Some(msg) = v.get("error").and_then(|x| x.as_str()) {
            return msg.to_string();
        }
    }
    if body.is_empty() {
        "空响应".to_string()
    } else {
        body.chars().take(200).collect()
    }
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    let client = reqwest::Client::builder()
        .timeout(std::time::Duration::from_secs(10))
        .build()
        .expect("HTTP 客户端初始化失败");
    log_line("MeTD 启动");
    tauri::Builder::default()
        // 单实例：已运行时再次点击快捷方式，只在原进程里唤起窗口，不再新建进程
        .plugin(tauri_plugin_single_instance::init(|app, _args, _cwd| {
            log_line("检测到重复启动，唤起已有窗口");
            if let Some(w) = app.get_webview_window("main") {
                let _ = w.unminimize();
                let _ = w.show();
                let _ = w.set_focus();
            }
        }))
        .plugin(tauri_plugin_autostart::init(
            tauri_plugin_autostart::MacosLauncher::LaunchAgent,
            None,
        ))
        .manage(ApiState {
            client,
            conn: Mutex::new(None),
        })
        .setup(|app| {
            let show = MenuItem::with_id(app, "show", "显示 MeTD", true, None::<&str>)?;
            let quit = MenuItem::with_id(app, "quit", "退出", true, None::<&str>)?;
            let menu = Menu::with_items(app, &[&show, &quit])?;
            let icon = app
                .default_window_icon()
                .cloned()
                .ok_or("缺少应用图标")?;
            TrayIconBuilder::with_id("metd-tray")
                .icon(icon)
                .tooltip("MeTD - Linux 流量监控")
                .menu(&menu)
                .show_menu_on_left_click(false)
                .on_menu_event(|app, event| match event.id.as_ref() {
                    "show" => {
                        if let Some(w) = app.get_webview_window("main") {
                            let _ = w.show();
                            let _ = w.set_focus();
                        }
                    }
                    "quit" => app.exit(0),
                    _ => {}
                })
                .on_tray_icon_event(|tray, event| {
                    if let TrayIconEvent::Click {
                        button: MouseButton::Left,
                        button_state: MouseButtonState::Up,
                        ..
                    } = event
                    {
                        let app = tray.app_handle();
                        if let Some(w) = app.get_webview_window("main") {
                            if w.is_visible().unwrap_or(false) {
                                let _ = w.hide();
                            } else {
                                let _ = w.show();
                                let _ = w.set_focus();
                            }
                        }
                    }
                })
                .build(app)?;
            Ok(())
        })
        .invoke_handler(tauri::generate_handler![
            ping,
            set_connection,
            clear_connection,
            api_get,
            api_post,
            save_capture,
            open_in_wireshark
        ])
        .run(tauri::generate_context!())
        .expect("tauri 应用启动失败");
}