// netmon-client Rust 侧：持有连接配置，通过 reqwest 访问远程 netmon API。
// 数据请求统一走 Rust 命令，令牌保存在进程状态中，避免 CORS 与令牌暴露在页面中。
use reqwest::header::AUTHORIZATION;
use serde_json::Value;
use std::sync::Mutex;
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

#[tauri::command]
fn set_connection(state: State<'_, ApiState>, base: String, token: String) -> Result<(), String> {
    let mut guard = state
        .conn
        .lock()
        .map_err(|_| "连接状态锁获取失败".to_string())?;
    *guard = Some(Conn { base, token });
    Ok(())
}

#[tauri::command]
fn clear_connection(state: State<'_, ApiState>) -> Result<(), String> {
    let mut guard = state
        .conn
        .lock()
        .map_err(|_| "连接状态锁获取失败".to_string())?;
    *guard = None;
    Ok(())
}

// 请求远程 API。path 需以 "/" 开头（如 /api/v1/traffic/now）。
#[tauri::command]
async fn api_get(state: State<'_, ApiState>, path: String) -> Result<Value, String> {
    let conn = {
        let guard = state
            .conn
            .lock()
            .map_err(|_| "连接状态锁获取失败".to_string())?;
        guard.clone()
    };
    let conn = conn.ok_or_else(|| "尚未配置服务器连接".to_string())?;

    let full = format!("{}{}", conn.base.trim_end_matches('/'), if path.starts_with('/') { path.as_str() } else { "/api/v1/traffic/now" });
    let mut req = state.client.get(&full);
    if !conn.token.is_empty() {
        req = req.header(AUTHORIZATION, format!("Bearer {}", conn.token));
    }
    let resp = req.send().await.map_err(|e| format!("请求失败: {e}"))?;
    let status = resp.status();
    let body = resp.text().await.unwrap_or_default();
    if !status.is_success() {
        return Err(format!("HTTP {}: {}", status.as_u16(), friendly_body(&body)));
    }
    serde_json::from_str(&body).map_err(|e| format!("响应解析失败: {e}"))
}

fn friendly_body(body: &str) -> String {
    if let Ok(v) = serde_json::from_str::<Value>(body) {
        if let Some(msg) = v.get("error").and_then(|x| x.as_str()) {
            return msg.to_string();
        }
    }
    if body.is_empty() { "空响应".to_string() } else { body.chars().take(200).collect() }
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    let client = reqwest::Client::builder()
        .timeout(std::time::Duration::from_secs(10))
        .build()
        .expect("HTTP 客户端初始化失败");
    tauri::Builder::default()
        .manage(ApiState {
            client,
            conn: Mutex::new(None),
        })
        .invoke_handler(tauri::generate_handler![
            set_connection,
            clear_connection,
            api_get
        ])
        .run(tauri::generate_context!())
        .expect("tauri 应用启动失败");
}