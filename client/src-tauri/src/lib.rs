// netmon-client Rust 侧：持有连接配置，通过 reqwest 访问远程 netmon API。
// 数据请求统一走 Rust 命令，令牌保存在进程状态中，避免 CORS 与令牌暴露在页面中。
use reqwest::header::AUTHORIZATION;
use serde_json::Value;
use std::fs::OpenOptions;
use std::io::Write;
use std::sync::Mutex;
use std::time::{Instant, SystemTime, UNIX_EPOCH};
use tauri::State;

#[derive(Clone)]
struct Conn {
    base: String,
    token: String,
}

struct ApiState {
    client: reqwest::Client,
    conn: Mutex<Option<Conn>>,
}

// 简单文件日志（写入系统临时目录 netmon-client.log），用于排查连接问题；不记录令牌明文。
fn log_line(msg: &str) {
    let ts = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_secs())
        .unwrap_or(0);
    let path = std::env::temp_dir().join("netmon-client.log");
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

// 请求远程 API。path 需以 "/" 开头（如 /api/v1/traffic/now）。
#[tauri::command]
async fn api_get(state: State<'_, ApiState>, path: String) -> Result<Value, String> {
    let started = Instant::now();
    log_line(&format!("api_get start path={path}"));
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
            log_line("api_get err: 尚未配置服务器连接");
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
    let mut req = state.client.get(&full);
    if !conn.token.is_empty() {
        req = req.header(AUTHORIZATION, format!("Bearer {}", conn.token));
    }
    match req.send().await {
        Ok(resp) => {
            let status = resp.status();
            let body = resp.text().await.unwrap_or_default();
            log_line(&format!(
                "api_get done path={path} status={} elapsed_ms={}",
                status.as_u16(),
                started.elapsed().as_millis()
            ));
            if !status.is_success() {
                return Err(format!("HTTP {}: {}", status.as_u16(), friendly_body(&body)));
            }
            serde_json::from_str(&body).map_err(|e| {
                log_line(&format!("api_get parse err: {e}"));
                format!("响应解析失败: {e}")
            })
        }
        Err(e) => {
            log_line(&format!(
                "api_get err path={path} elapsed_ms={} err={e}",
                started.elapsed().as_millis()
            ));
            Err(format!("请求失败: {e}"))
        }
    }
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
    log_line("netmon-client 启动");
    tauri::Builder::default()
        .manage(ApiState {
            client,
            conn: Mutex::new(None),
        })
        .invoke_handler(tauri::generate_handler![
            ping,
            set_connection,
            clear_connection,
            api_get
        ])
        .run(tauri::generate_context!())
        .expect("tauri 应用启动失败");
}