// MeTD Rust 侧：持有连接配置、托盘与自启能力，通过 reqwest 访问远程 netmon API。
use reqwest::header::AUTHORIZATION;
use serde_json::Value;
use std::fs::OpenOptions;
use std::io::Write;
use std::process::{Child, Command, Stdio};
use std::sync::Mutex;
use std::time::{Instant, SystemTime, UNIX_EPOCH};
use tauri::menu::{Menu, MenuItem};
use tauri::tray::{MouseButton, MouseButtonState, TrayIconBuilder, TrayIconEvent};
use tauri::{Emitter, Manager, State};

#[derive(Clone)]
struct Conn {
    base: String,
    token: String,
}

struct ApiState {
    client: reqwest::Client,
    conn: Mutex<Option<Conn>>,
    // 实时推送任务（订阅 /api/v1/stream）
    stream: Mutex<Option<tauri::async_runtime::JoinHandle<()>>>,
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
async fn request_json(
    state: &ApiState,
    path: String,
    post: bool,
    body: Option<Value>,
) -> Result<Value, String> {
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
        let r = state.client.post(&full);
        match &body {
            Some(v) => r.json(v),
            None => r,
        }
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
    request_json(&state, path, false, None).await
}

// POST 命令（无请求体）：用于开始抓包等动作类接口。
#[tauri::command]
async fn api_post(state: State<'_, ApiState>, path: String) -> Result<Value, String> {
    request_json(&state, path, true, None).await
}

// 带 JSON 请求体的 POST：用于执行运维动作等需要传参的接口。
// 把服务端的任意文件下载到本机临时目录（%TEMP%\MeTD-files），返回保存路径。
// 订阅服务端实时通道（SSE），把 snapshot / alert 事件推给前端。
// 断线会自动重连（3 秒退避），直到调用 stop_stream。
#[tauri::command]
fn start_stream(app: tauri::AppHandle, state: State<'_, ApiState>) -> Result<(), String> {
    stop_stream(state.clone())?;
    let client = state.client.clone();
    let conn = {
        let guard = state.conn.lock().map_err(|_| "连接状态锁获取失败".to_string())?;
        guard.clone()
    };
    let conn = conn.ok_or_else(|| "尚未配置服务器连接".to_string())?;
    let handle = tauri::async_runtime::spawn(async move {
        loop {
            let url = format!("{}/api/v1/stream", conn.base.trim_end_matches('/'));
            let mut req = client.get(&url);
            if !conn.token.is_empty() {
                req = req.header(AUTHORIZATION, format!("Bearer {}", conn.token));
            }
            match req.send().await {
                Ok(mut resp) if resp.status().is_success() => {
                    log_line("stream connected");
                    let mut buf = String::new();
                    while let Ok(Some(chunk)) = resp.chunk().await {
                        buf.push_str(&String::from_utf8_lossy(&chunk));
                        while let Some(idx) = buf.find("\n\n") {
                            let raw = buf[..idx].to_string();
                            buf.drain(..idx + 2);
                            let mut event = String::from("message");
                            let mut data = String::new();
                            for line in raw.lines() {
                                if let Some(v) = line.strip_prefix("event: ") {
                                    event = v.trim().to_string();
                                } else if let Some(v) = line.strip_prefix("data: ") {
                                    data = v.trim().to_string();
                                }
                            }
                            if data.is_empty() {
                                continue;
                            }
                            if let Ok(value) = serde_json::from_str::<Value>(&data) {
                                let _ = app.emit(&event, value);
                            }
                        }
                    }
                    log_line("stream ended, will reconnect");
                }
                Ok(resp) => log_line(&format!("stream http {}", resp.status().as_u16())),
                Err(e) => log_line(&format!("stream error: {e}")),
            }
            // 退避 3 秒后重连（只发生在连接断开时；在异步任务里短暂阻塞是可接受的）
            std::thread::sleep(std::time::Duration::from_secs(3));
        }
    });
    let mut guard = state.stream.lock().map_err(|_| "连接状态锁获取失败".to_string())?;
    *guard = Some(handle);
    Ok(())
}

// 停止实时推送订阅。
#[tauri::command]
fn stop_stream(state: State<'_, ApiState>) -> Result<(), String> {
    let mut guard = state.stream.lock().map_err(|_| "连接状态锁获取失败".to_string())?;
    if let Some(h) = guard.take() {
        h.abort();
        log_line("stream stopped");
    }
    Ok(())
}

// 多机探测：并发（顺序）访问多台服务器的 /traffic/now，返回在线状态、版本、能力与实时指标。
#[tauri::command]
async fn probe_hosts(state: State<'_, ApiState>, hosts: Vec<Value>) -> Result<Value, String> {
    let mut out = Vec::new();
    for h in hosts {
        let name = h.get("name").and_then(|v| v.as_str()).unwrap_or("").to_string();
        let base = h.get("base").and_then(|v| v.as_str()).unwrap_or("").to_string();
        let token = h.get("token").and_then(|v| v.as_str()).unwrap_or("").to_string();
        let mut item = serde_json::json!({ "name": name, "base": base, "ok": false });
        if base.is_empty() {
            item["error"] = serde_json::json!("地址为空");
            out.push(item);
            continue;
        }
        let url = format!("{}/api/v1/traffic/now", base.trim_end_matches('/'));
        let mut req = state.client.get(&url).timeout(std::time::Duration::from_secs(3));
        if !token.is_empty() {
            req = req.header(AUTHORIZATION, format!("Bearer {}", token));
        }
        match req.send().await {
            Ok(resp) if resp.status().is_success() => {
                let body = resp.text().await.unwrap_or_default();
                match serde_json::from_str::<Value>(&body) {
                    Ok(v) => {
                        item["ok"] = serde_json::json!(true);
                        item["now"] = v;
                    }
                    Err(e) => item["error"] = serde_json::json!(format!("解析失败: {e}")),
                }
            }
            Ok(resp) => item["error"] = serde_json::json!(format!("HTTP {}", resp.status().as_u16())),
            Err(e) => item["error"] = serde_json::json!(format!("{e}")),
        }
        out.push(item);
    }
    Ok(Value::Array(out))
}

// ---------------- 本机引擎（netmon.exe sidecar，离线 pcap 回放） ----------------
// 「数据来源 = 离线 pcap 文件」的落地方式：把选中的 pcap 交给本机 netmon.exe 回放，
// 由它分配一个只监听 127.0.0.1 的 HTTP 地址，前端再用原有连接流程连上去。
// 本机实时抓包（source = local）在采集端尚未实现，这里只返回明确的错误。

// 返回给前端的本机引擎信息（字段名与 client/src/api.js 的约定一致）
#[derive(Clone, serde::Serialize)]
struct LocalEngineInfo {
    base: String,
    pid: u32,
    data_dir: String,
    mode: String,
}

// 正在运行的本机引擎：进程句柄 + 对外信息
struct LocalEngine {
    info: LocalEngineInfo,
    child: Child,
}

// 由 Tauri 托管的本机引擎状态（同一时刻最多一个）
struct LocalEngineState {
    engine: Mutex<Option<LocalEngine>>,
}

// 本机引擎数据目录：%LOCALAPPDATA%\MeTD\local-data，
// 退回 %USERPROFILE%\AppData\Local\MeTD\local-data（与 client/src/localsource.js 的规则一致）
fn local_data_dir() -> Result<std::path::PathBuf, String> {
    let local_app_data = std::env::var("LOCALAPPDATA")
        .ok()
        .map(|s| s.trim().to_string())
        .filter(|s| !s.is_empty());
    let base = match local_app_data {
        Some(dir) => dir,
        None => {
            let profile = std::env::var("USERPROFILE")
                .ok()
                .map(|s| s.trim().to_string())
                .filter(|s| !s.is_empty())
                .ok_or_else(|| "无法确定本机数据目录（缺少 LOCALAPPDATA / USERPROFILE 环境变量）".to_string())?;
            format!("{}\\AppData\\Local", profile.trim_end_matches(|c| c == '\\' || c == '/'))
        }
    };
    Ok(std::path::PathBuf::from(base.trim_end_matches(|c| c == '\\' || c == '/'))
        .join("MeTD")
        .join("local-data"))
}

// 找一个空闲端口：绑定 127.0.0.1:0 让系统分配后立刻释放，把端口号交给 netmon
fn free_local_port() -> Result<u16, String> {
    let listener = std::net::TcpListener::bind("127.0.0.1:0").map_err(|e| format!("无法分配本机端口：{e}"))?;
    let port = listener
        .local_addr()
        .map_err(|e| format!("无法读取本机端口：{e}"))?
        .port();
    drop(listener);
    Ok(port)
}

// 找本机 netmon.exe：① 应用资源目录 ② 可执行文件同目录 ③ 开发时的 deploy/windows
fn resolve_netmon_exe(app: &tauri::AppHandle) -> Result<std::path::PathBuf, String> {
    let mut candidates: Vec<std::path::PathBuf> = Vec::new();
    if let Ok(dir) = app.path().resource_dir() {
        candidates.push(dir.join("netmon.exe"));
    }
    if let Ok(exe) = std::env::current_exe() {
        if let Some(parent) = exe.parent() {
            candidates.push(parent.join("netmon.exe"));
        }
    }
    candidates.push(
        std::path::Path::new(env!("CARGO_MANIFEST_DIR"))
            .join("..")
            .join("..")
            .join("deploy")
            .join("windows")
            .join("netmon.exe"),
    );
    for c in &candidates {
        if c.is_file() {
            return Ok(c.clone());
        }
    }
    let looked = candidates
        .iter()
        .map(|c| c.display().to_string())
        .collect::<Vec<_>>()
        .join("、");
    Err(format!(
        "未找到本机 netmon.exe：请把 netmon.exe 放到应用安装目录或 deploy/windows 下（已查找：{looked}）"
    ))
}

// 停掉当前本机引擎，返回是否真的停掉了一个进程
fn stop_local_engine(state: &LocalEngineState) -> Result<bool, String> {
    let mut guard = state
        .engine
        .lock()
        .map_err(|_| "本机引擎状态锁获取失败".to_string())?;
    let Some(mut running) = guard.take() else {
        return Ok(false);
    };
    let pid = running.info.pid;
    let killed = running.child.kill().is_ok();
    // wait() 回收句柄，避免留下僵尸进程
    let _ = running.child.wait();
    log_line(&format!("local engine stopped pid={pid} killed={killed}"));
    Ok(true)
}

// 启动本机引擎并等它就绪：mode=offline 时需要 pcap_path，local/remote 直接报错。
// 参数与实际传给 netmon 的完全一致：-replay-file <pcap> -data-dir <dir> -listen 127.0.0.1:<port>
#[tauri::command]
async fn local_engine_start(
    app: tauri::AppHandle,
    mode: String,
    pcap_path: Option<String>,
) -> Result<LocalEngineInfo, String> {
    let mode = mode.trim().to_string();
    match mode.as_str() {
        "remote" => return Err("远程模式不需要启动本机引擎".to_string()),
        "local" => {
            return Err(
                "本机实时抓包尚未实现（采集端 Windows 抓包开发中），请改用「离线 pcap 文件」或连接远程 Linux"
                    .to_string(),
            )
        }
        "offline" => {}
        other => return Err(format!("未知的数据来源模式：{other}")),
    }
    let pcap = pcap_path.unwrap_or_default().trim().to_string();
    if pcap.is_empty() {
        return Err("离线 pcap 模式必须选择 pcap 文件".to_string());
    }
    let pcap_path_buf = std::path::PathBuf::from(&pcap);
    if !pcap_path_buf.is_file() {
        return Err(format!("pcap 文件不存在或不是文件：{pcap}"));
    }

    let (exe, data_dir, port) = {
        let engine_state = app.state::<LocalEngineState>();
        // 重复点击「启动」时先停掉上一个，保证同一时刻只有一个本机引擎
        let _ = stop_local_engine(&engine_state);
        let exe = resolve_netmon_exe(&app)?;
        let data_dir = local_data_dir()?;
        std::fs::create_dir_all(&data_dir).map_err(|e| format!("创建数据目录失败 {}：{e}", data_dir.display()))?;
        let port = free_local_port()?;
        (exe, data_dir, port)
    };

    let listen = format!("127.0.0.1:{port}");
    let base = format!("http://{listen}");
    // netmon 的输出重定向到数据目录里的日志，健康检查失败时可以排查
    let log_path = data_dir.join("netmon-engine.log");
    let mut cmd = Command::new(&exe);
    cmd.arg("-replay-file")
        .arg(&pcap)
        .arg("-data-dir")
        .arg(&data_dir)
        .arg("-listen")
        .arg(&listen)
        .stdin(Stdio::null());
    match std::fs::File::create(&log_path) {
        Ok(log) => {
            let err_log = log.try_clone();
            cmd.stdout(Stdio::from(log));
            if let Ok(err_log) = err_log {
                cmd.stderr(Stdio::from(err_log));
            }
        }
        Err(_) => {
            cmd.stdout(Stdio::null());
            cmd.stderr(Stdio::null());
        }
    }
    // Windows 下不弹出控制台窗口（CREATE_NO_WINDOW）
    #[cfg(windows)]
    {
        use std::os::windows::process::CommandExt;
        const CREATE_NO_WINDOW: u32 = 0x0800_0000;
        cmd.creation_flags(CREATE_NO_WINDOW);
    }
    log_line(&format!(
        "local_engine_start exe={} pcap={} data_dir={} listen={}",
        exe.display(),
        pcap,
        data_dir.display(),
        listen
    ));
    let mut child = cmd
        .spawn()
        .map_err(|e| format!("启动本机 netmon 失败（{}）：{e}", exe.display()))?;
    let pid = child.id();

    let info = LocalEngineInfo {
        base: base.clone(),
        pid,
        data_dir: data_dir.display().to_string(),
        mode: mode.clone(),
    };

    // 轮询 /api/v1/health 等它就绪，最多约 5 秒
    let client = reqwest::Client::new();
    let health_url = format!("{base}/api/v1/health");
    let mut ready = false;
    let mut exited: Option<String> = None;
    for _ in 0..25 {
        if let Ok(resp) = client
            .get(&health_url)
            .timeout(std::time::Duration::from_millis(800))
            .send()
            .await
        {
            if resp.status().is_success() {
                ready = true;
                break;
            }
        }
        // 进程已经退出（例如 pcap 打不开）就没必要等满 5 秒
        if let Ok(Some(status)) = child.try_wait() {
            exited = Some(format!("{status}"));
            break;
        }
        // 与 start_stream 中的退避一样：异步任务里短暂阻塞是可接受的
        std::thread::sleep(std::time::Duration::from_millis(200));
    }

    if !ready {
        let _ = child.kill();
        let _ = child.wait();
        let hint = format!("详见日志 {}", log_path.display());
        return Err(match exited {
            Some(code) => format!("本机 netmon 启动即退出（退出状态 {code}），{hint}"),
            None => format!("本机 netmon 启动超时（5 秒内未就绪），{hint}"),
        });
    }

    let engine_state = app.state::<LocalEngineState>();
    let mut guard = engine_state
        .engine
        .lock()
        .map_err(|_| "本机引擎状态锁获取失败".to_string())?;
    *guard = Some(LocalEngine {
        info: info.clone(),
        child,
    });
    log_line(&format!("local engine ready base={base} pid={pid}"));
    Ok(info)
}

// 停止本机引擎：结束进程并清空状态，返回是否真的停掉了一个进程
#[tauri::command]
fn local_engine_stop(app: tauri::AppHandle) -> Result<bool, String> {
    let engine_state = app.state::<LocalEngineState>();
    stop_local_engine(&engine_state)
}

// 查询本机引擎状态：没有运行时返回 null
#[tauri::command]
fn local_engine_status(app: tauri::AppHandle) -> Result<Option<LocalEngineInfo>, String> {
    let engine_state = app.state::<LocalEngineState>();
    let guard = engine_state
        .engine
        .lock()
        .map_err(|_| "本机引擎状态锁获取失败".to_string())?;
    Ok(guard.as_ref().map(|e| e.info.clone()))
}

// 不允许用系统默认程序打开的可执行 / 脚本类扩展名。
// 服务端可以决定下载文件的文件名，而 `cmd /C start` 会把它交给系统关联程序（可能是执行），
// 因此这里做一道黑名单；名单与前端 client/src/files.js 的 DANGEROUS_EXTS 逐项保持一致
// （改一处就要改另一处，client/test/files.test.mjs 只覆盖前端那份）。
const BLOCKED_EXTS: [&str; 24] = [
    "exe", "bat", "cmd", "com", "scr", "pif", "msi", "msp", "cpl", "ps1", "psm1", "vbs", "vbe",
    "js", "jse", "wsf", "wsh", "hta", "lnk", "url", "reg", "jar", "chm", "inf",
];

// 取小写扩展名（不含点）。按「最后一个点」取，因此 report.pdf.exe 会得到 exe。
fn lower_ext(name: &str) -> String {
    let base = name.rsplit(['/', '\\']).next().unwrap_or(name);
    match base.rsplit_once('.') {
        Some((stem, ext)) if !stem.is_empty() && !ext.is_empty() => ext.to_ascii_lowercase(),
        _ => String::new(),
    }
}

fn is_blocked_ext(name: &str) -> bool {
    let ext = lower_ext(name);
    !ext.is_empty() && BLOCKED_EXTS.contains(&ext.as_str())
}

// 找一个不覆盖已有文件的落盘路径：服务端可以在响应头里重复给出同一个文件名，
// 若直接覆盖，攻击者就能把「用户以前下载过、看起来可信」的文件悄悄换成别的内容。
fn free_dest_path(dir: &std::path::Path, name: &str) -> std::path::PathBuf {
    let first = dir.join(name);
    if !first.exists() {
        return first;
    }
    for i in 1..1000 {
        let cand = dir.join(format!("{i}-{name}"));
        if !cand.exists() {
            return cand;
        }
    }
    first
}

// ---------------- 临时文件清理 ----------------
// 两个下载目录（%TEMP%\MeTD-files、%TEMP%\MeTD-captures）只增不减，长期使用会一直占磁盘。
// 这里只清 MeTD 自己创建的目录：按修改时间保留最新 keep 个，额外丢弃 6 小时前的陈旧文件
// （防止用户刚下载、还打算打开的文件被立刻删掉）。
//
// 剪枝逻辑刻意与具体临时目录解耦，便于单元测试直接传入任意目录。
const PRUNE_KEEP: usize = 20;
const PRUNE_MAX_AGE: std::time::Duration = std::time::Duration::from_secs(6 * 60 * 60);

fn prune_old_files(dir: &std::path::Path, keep: usize, max_age: std::time::Duration) -> usize {
    let Ok(entries) = std::fs::read_dir(dir) else {
        return 0;
    };
    let mut files: Vec<(std::time::SystemTime, std::path::PathBuf)> = entries
        .flatten()
        .filter_map(|e| {
            let meta = e.metadata().ok()?;
            if !meta.is_file() {
                return None;
            }
            let mtime = meta.modified().unwrap_or(std::time::SystemTime::UNIX_EPOCH);
            Some((mtime, e.path()))
        })
        .collect();
    // 新的在前
    files.sort_by(|a, b| b.0.cmp(&a.0));
    let cutoff = std::time::SystemTime::now()
        .checked_sub(max_age)
        .unwrap_or(std::time::SystemTime::UNIX_EPOCH);
    let mut removed = 0usize;
    for (i, (mtime, path)) in files.into_iter().enumerate() {
        if i < keep && *mtime >= cutoff {
            continue;
        }
        if std::fs::remove_file(&path).is_ok() {
            removed += 1;
        }
    }
    removed
}

/// 启动时清理 MeTD 自己的两个临时下载目录，返回删除的文件数。
fn cleanup_temp_dirs() -> usize {
    let mut removed = 0usize;
    for name in ["MeTD-files", "MeTD-captures"] {
        let dir = std::env::temp_dir().join(name);
        if dir.is_dir() {
            removed += prune_old_files(&dir, PRUNE_KEEP, PRUNE_MAX_AGE);
        }
    }
    if removed > 0 {
        log_line(&format!("临时文件清理：删除 {removed} 个过期文件"));
    }
    removed
}

// 单次下载上限（与服务端 internal/api/files.go 的 maxDownloadBytes 对齐）。
const MAX_DOWNLOAD_BYTES: u64 = 64 << 20;
// 抓包文件单独放宽：正常抓包会明显大于普通文件，但同样不能无上限（否则恶意/异常服务端
// 可以用超大响应把内存与临时目录打满）。
const MAX_CAPTURE_BYTES: u64 = 256 << 20;

#[tauri::command]
async fn save_server_file(state: State<'_, ApiState>, path: String, name: String) -> Result<String, String> {
    let conn = {
        let guard = state
            .conn
            .lock()
            .map_err(|_| "连接状态锁获取失败".to_string())?;
        guard.clone()
    };
    let conn = conn.ok_or_else(|| "尚未配置服务器连接".to_string())?;
    let url = format!("{}{}", conn.base.trim_end_matches('/'), if path.starts_with('/') { path.as_str() } else { "" });
    let mut req = state.client.get(&url);
    if !conn.token.is_empty() {
        req = req.header(AUTHORIZATION, format!("Bearer {}", conn.token));
    }
    let resp = req.send().await.map_err(|e| format!("下载失败: {e}"))?;
    if !resp.status().is_success() {
        return Err(format!("下载失败: HTTP {}", resp.status().as_u16()));
    }
    // 超限直接拒绝下载，避免异常/恶意服务端把内存或磁盘打满。
    if let Some(len) = resp.content_length() {
        if len > MAX_DOWNLOAD_BYTES {
            return Err(format!(
                "文件过大（{} MB），上限 {} MB",
                len >> 20,
                MAX_DOWNLOAD_BYTES >> 20
            ));
        }
    }
    let bytes = resp.bytes().await.map_err(|e| format!("读取响应失败: {e}"))?;
    if bytes.len() as u64 > MAX_DOWNLOAD_BYTES {
        return Err(format!(
            "文件过大（{} MB），上限 {} MB",
            bytes.len() >> 20,
            MAX_DOWNLOAD_BYTES >> 20
        ));
    }
    let dir = std::env::temp_dir().join("MeTD-files");
    std::fs::create_dir_all(&dir).map_err(|e| format!("创建目录失败: {e}"))?;
    let safe: String = name
        .chars()
        .map(|c| if c.is_alphanumeric() || "._-".contains(c) { c } else { '_' })
        .collect();
    let safe = if safe.is_empty() { "download.bin".to_string() } else { safe };
    let dest = free_dest_path(&dir, &safe);
    std::fs::write(&dest, &bytes).map_err(|e| format!("保存文件失败: {e}"))?;
    log_line(&format!("save_server_file {} bytes -> {}", bytes.len(), dest.display()));
    Ok(dest.to_string_lossy().to_string())
}

// 用系统默认程序打开本地文件。
//
// 安全约束：可执行 / 脚本类扩展名一律拒绝——本命令的实现是 `cmd /C start`，
// 等于把文件交给系统关联程序（可能直接执行）。调用方（下载完自动打开）曾经让它成为
// 「点一次下载 = 执行服务端指定的文件」的代码执行链，因此这里必须自己拒。
#[tauri::command]
fn open_local_file(path: String) -> Result<String, String> {
    if !std::path::Path::new(&path).exists() {
        return Err("文件不存在".to_string());
    }
    let name = std::path::Path::new(&path)
        .file_name()
        .map(|s| s.to_string_lossy().to_string())
        .unwrap_or_default();
    if is_blocked_ext(&name) {
        return Err(format!(
            "为安全起见不打开可执行/脚本类文件（.{}）：{path}",
            lower_ext(&name)
        ));
    }
    if std::process::Command::new("cmd").args(["/C", "start", "", path.as_str()]).spawn().is_ok() {
        return Ok(format!("已用默认程序打开：{path}"));
    }
    Err(format!("打开失败，请手动打开：{path}"))
}

#[tauri::command]
async fn api_post_json(state: State<'_, ApiState>, path: String, body: Value) -> Result<Value, String> {
    request_json(&state, path, true, Some(body)).await
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
    // 抓包文件也必须有上限：原先直接 resp.bytes() 全读进内存，服务端返回一个超大响应
    // 就会把客户端内存打满。
    if let Some(len) = resp.content_length() {
        if len > MAX_CAPTURE_BYTES {
            return Err(format!(
                "抓包文件过大（{} MB），上限 {} MB",
                len >> 20,
                MAX_CAPTURE_BYTES >> 20
            ));
        }
    }
    let bytes = resp.bytes().await.map_err(|e| format!("读取响应失败: {e}"))?;
    if bytes.len() as u64 > MAX_CAPTURE_BYTES {
        return Err(format!(
            "抓包文件过大（{} MB），上限 {} MB",
            bytes.len() >> 20,
            MAX_CAPTURE_BYTES >> 20
        ));
    }
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
            stream: Mutex::new(None),
        })
        // 本机引擎（离线 pcap 回放用的 netmon.exe）状态
        .manage(LocalEngineState {
            engine: Mutex::new(None),
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
            // 启动时清一次 MeTD 自己的临时下载目录（只留最新 20 个、且丢弃 6 小时前的）
            let _ = cleanup_temp_dirs();
            Ok(())
        })
        .invoke_handler(tauri::generate_handler![
            ping,
            set_connection,
            clear_connection,
            api_get,
            api_post,
            api_post_json,
            save_server_file,
            start_stream,
            probe_hosts,
            stop_stream,
            open_local_file,
            save_capture,
            open_in_wireshark,
            local_engine_start,
            local_engine_stop,
            local_engine_status
        ])
        .run(tauri::generate_context!())
        .expect("tauri 应用启动失败");
}

// ---------------- 单元测试 ----------------
// 这些测试覆盖纯逻辑函数（不依赖 Tauri 运行时 / 网络），可用 `cargo test` 单独验证。
#[cfg(test)]
mod tests {
    use super::*;
    use std::time::{Duration, SystemTime, UNIX_EPOCH};

    /// 用 Win32 SetFileTime 把文件的最后修改时间改到 1 小时前。
    /// 必须等句柄关闭后 FS 才会刷新目录里的时间戳，所以后面的读目录操作会看到新时间。
    #[cfg(windows)]
    fn set_mtime_old(path: &std::path::Path) {
        use std::os::windows::ffi::OsStrExt;
        const FILE_WRITE_ATTRIBUTES: u32 = 0x0100;
        const OPEN_EXISTING: u32 = 3;
        const FILE_ATTRIBUTE_NORMAL: u32 = 0x0000_0080;
        extern "system" {
            fn CreateFileW(
                lp_file_name: *const u16,
                dw_desired_access: u32,
                dw_share_mode: u32,
                lp_security_attributes: *mut core::ffi::c_void,
                dw_creation_disposition: u32,
                dw_flags_and_attributes: u32,
                h_template_file: *mut core::ffi::c_void,
            ) -> *mut core::ffi::c_void;
            fn SetFileTime(
                h_file: *mut core::ffi::c_void,
                lp_creation_time: *const u64,
                lp_last_access_time: *const u64,
                lp_last_write_time: *const u64,
            ) -> i32;
            fn CloseHandle(h_object: *mut core::ffi::c_void) -> i32;
        }
        let mut wide: Vec<u16> = path.as_os_str().encode_wide().collect();
        wide.push(0);
        unsafe {
            let h = CreateFileW(
                wide.as_ptr(),
                FILE_WRITE_ATTRIBUTES,
                0,
                std::ptr::null_mut(),
                OPEN_EXISTING,
                FILE_ATTRIBUTE_NORMAL,
                std::ptr::null_mut(),
            );
            // INVALID_HANDLE_VALUE == -1
            if h as isize == -1 {
                return;
            }
            let secs = SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .unwrap_or_default()
                .as_secs()
                .saturating_sub(3600);
            // FILETIME 单位是 100ns，起点 1601-01-01（与 UNIX 纪元差 11644473600 秒）
            let ticks: u64 = (secs + 11_644_473_600) * 10_000_000;
            SetFileTime(h, std::ptr::null(), std::ptr::null(), &ticks);
            CloseHandle(h);
        }
    }

    #[test]
    fn prune_keeps_newest_files() {
        let dir = std::env::temp_dir().join(format!("metd-prune-keep-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&dir);
        std::fs::create_dir_all(&dir).expect("建目录");
        for name in ["a.pcap", "b.pcap", "c.pcap"] {
            std::fs::write(dir.join(name), b"x").expect("写文件");
        }
        #[cfg(windows)]
        set_mtime_old(&dir.join("a.pcap"));
        let removed = prune_old_files(&dir, 2, Duration::from_secs(24 * 3600));
        assert_eq!(removed, 1, "只应删掉最旧的 1 个");
        assert!(!dir.join("a.pcap").exists(), "最旧的文件应被删除");
        assert!(dir.join("b.pcap").exists());
        assert!(dir.join("c.pcap").exists());
        let _ = std::fs::remove_dir_all(&dir);
    }

    #[test]
    fn prune_drops_stale_files_beyond_keep() {
        let dir = std::env::temp_dir().join(format!("metd-prune-age-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&dir);
        std::fs::create_dir_all(&dir).expect("建目录");
        std::fs::write(dir.join("old.pcap"), b"x").expect("写文件");
        #[cfg(windows)]
        set_mtime_old(&dir.join("old.pcap"));
        // max_age = 0：任何非“现在”的文件都算过期；keep 给足，验证年龄这一路生效
        let removed = prune_old_files(&dir, 10, Duration::from_secs(0));
        assert!(removed >= 1, "过期文件应被删除，实际删除 {removed} 个");
        assert!(!dir.join("old.pcap").exists());
        let _ = std::fs::remove_dir_all(&dir);
    }

    #[test]
    fn prune_ignores_subdirectories() {
        let dir = std::env::temp_dir().join(format!("metd-prune-dir-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&dir);
        std::fs::create_dir_all(dir.join("sub")).expect("建子目录");
        let removed = prune_old_files(&dir, 0, Duration::from_secs(0));
        assert_eq!(removed, 0, "目录不该被当成文件删除");
        assert!(dir.join("sub").is_dir(), "子目录应保留");
        let _ = std::fs::remove_dir_all(&dir);
    }

    #[test]
    fn prune_on_missing_dir_is_noop() {
        let dir = std::env::temp_dir().join(format!("metd-prune-missing-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&dir);
        assert_eq!(prune_old_files(&dir, 5, Duration::from_secs(60)), 0);
    }
}