use axum::{extract::{Path, Query, State}, http::{HeaderMap, StatusCode}, response::IntoResponse, routing::{get, post}, Json, Router};
use serde::Deserialize;
use serde_json::Value as JsonValue;
use serde_json::{json, Value};
use std::{process::Stdio, sync::Arc, time::Duration};
use tokio::{process::Command, time::timeout};
use tower_http::trace::TraceLayer;

#[derive(Clone)] struct AgentState { token: Option<Arc<String>> }
#[derive(Deserialize)] struct Lines { lines: Option<usize> }
#[derive(Deserialize)] struct ActionBody { params: Option<JsonValue> }

#[tokio::main]
async fn main() -> anyhow::Result<()> {
    tracing_subscriber::fmt().with_env_filter("info,tower_http=info").init();
    let bind = std::env::var("DASHBOARD_AGENT_BIND").unwrap_or_else(|_| "172.17.0.1:13001".into());
    let token = read_secret("DASHBOARD_AGENT_TOKEN", "DASHBOARD_AGENT_TOKEN_FILE").map(Arc::new);
    if token.is_none() { tracing::warn!("DASHBOARD_AGENT_TOKEN not configured; local network protection only"); }
    let app = Router::new()
        .route("/v1/health", get(health))
        .route("/v1/overview", get(overview)).route("/v1/services", get(services)).route("/v1/logs/:source", get(logs)).route("/v1/actions/:id/run", post(run_action))
        .layer(TraceLayer::new_for_http()).with_state(AgentState { token });
    let listener = tokio::net::TcpListener::bind(&bind).await?;
    tracing::info!(%bind, "dashboard agent listening");
    axum::serve(listener, app).await?;
    Ok(())
}

async fn health(State(st): State<AgentState>, headers: HeaderMap) -> Result<Json<Value>, AgentError> { auth(&st, &headers)?; Ok(Json(json!({"ok":true}))) }
async fn overview(State(st): State<AgentState>, headers: HeaderMap) -> Result<Json<Value>, AgentError> { auth(&st, &headers)?; Ok(Json(overview_value().await)) }
async fn overview_value() -> Value {
    let hostname = cmd("hostname", &[], None, 5).await; let uptime = cmd("uptime", &["-p"], None, 5).await; let load = std::fs::read_to_string("/proc/loadavg").unwrap_or_default(); let mem = std::fs::read_to_string("/proc/meminfo").unwrap_or_default(); let disk = cmd("df", &["-B1", "/"], None, 5).await; let failed = cmd("systemctl", &["--failed", "--no-legend", "--plain"], None, 8).await; let docker = cmd("docker", &["ps", "--format", "{{.Names}}"], None, 8).await; let ark = ark_status().await;
    json!({"hostname": hostname.stdout.trim(), "uptime": uptime.stdout.trim(), "loadavg": load.split_whitespace().take(3).collect::<Vec<_>>().join(" "), "memory": parse_mem(&mem), "disk": parse_df(&disk.stdout), "failed_units": failed.stdout.lines().filter(|l| !l.trim().is_empty()).count(), "docker_running": docker.stdout.lines().filter(|l| !l.trim().is_empty()).count(), "ark": ark})
}
async fn services(State(st): State<AgentState>, headers: HeaderMap) -> Result<Json<Vec<Value>>, AgentError> { auth(&st, &headers)?; Ok(Json(services_value().await)) }
async fn services_value() -> Vec<Value> {
    let systemd_specs = [
        ("tailscaled", "tailscaled", "Host", "primary", "systemd", "tailscaled"),
        ("docker", "Docker", "Host", "primary", "systemd", "docker"),
        ("apps", "Euripus compose stack", "Euripus", "supporting", "compose", "docker-compose@apps"),
        ("edge", "Edge compose stack", "Edge / ingress", "supporting", "compose", "docker-compose@edge"),
        ("ark", "ARK compose stack", "ARK", "primary", "compose", "docker-compose@ark"),
        ("dns", "AdGuard Home DNS compose stack", "AdGuard Home DNS", "supporting", "compose", "docker-compose@dns"),
        ("home-finder", "Home Finder compose stack", "Home Finder / Luleå Rental Finder", "supporting", "compose", "docker-compose@home-finder"),
        ("wol", "Wake-on-LAN compose stack", "Wake-on-LAN HTTP service", "supporting", "compose", "docker-compose@wol"),
        ("dashboard-agent", "Dashboard host agent", "Oliver Dashboard", "supporting", "systemd", "dashboard-agent"),
    ];
    let container_specs = [
        ("apps-web-1", "Euripus web", "Euripus", "primary"),
        ("apps-server-1", "Euripus server", "Euripus", "primary"),
        ("apps-sports-api-1", "Euripus sports API", "Euripus", "primary"),
        ("apps-postgres-1", "Postgres", "Euripus", "supporting"),
        ("apps-meilisearch-1", "Meilisearch", "Euripus", "supporting"),
        ("apps-gluetun-1", "Gluetun", "Euripus", "supporting"),
        ("oliver-dashboard", "Oliver Dashboard", "Oliver Dashboard", "primary"),
        ("home-finder-app-1", "Home Finder / Luleå Rental Finder", "Home Finder / Luleå Rental Finder", "primary"),
        ("dns-adguardhome-1", "AdGuard Home DNS", "AdGuard Home DNS", "primary"),
        ("wol-wol-http-1", "Wake-on-LAN HTTP", "Wake-on-LAN HTTP service", "primary"),
        ("edge-caddy-1", "Caddy ingress", "Edge / ingress", "primary"),
        ("edge-cloudflare-ddns-1", "Cloudflare DDNS", "Edge / ingress", "supporting"),
        ("edge-cloudflare-ddns-ark-1", "Cloudflare DDNS ARK", "Edge / ingress", "supporting"),
        ("edge-cloudflare-ddns-dot-1", "Cloudflare DDNS DoT", "Edge / ingress", "supporting"),
    ];
    let mut rows = Vec::new();
    for (name, display_name, group, role, kind, unit) in systemd_specs {
        let active = cmd("systemctl", &["is-active", unit], None, 5).await.stdout.trim().to_string();
        let enabled = cmd("systemctl", &["is-enabled", unit], None, 5).await.stdout.trim().to_string();
        rows.push(json!({"name": name, "display_name": display_name, "group": group, "role": role, "kind": kind, "unit": unit, "status": active, "enabled": enabled, "action_key": name}));
    }
    for (name, display_name, group, role) in container_specs {
        rows.push(json!({"name": name, "display_name": display_name, "group": group, "role": role, "kind": "container", "unit": name, "status": container_status(name).await, "enabled": "compose", "action_key": name}));
    }
    rows
}
async fn logs(State(st): State<AgentState>, headers: HeaderMap, Path(source): Path<String>, Query(q): Query<Lines>) -> Result<Json<Value>, AgentError> {
    auth(&st, &headers)?; let lines_s = q.lines.unwrap_or(200).min(1000).to_string();
    let res = match source.as_str() {
        "tailscaled" => cmd("journalctl", &["-u", "tailscaled", "-n", &lines_s, "--no-pager", "--output", "short-iso"], None, 15).await,
        "docker" => cmd("journalctl", &["-u", "docker", "-n", &lines_s, "--no-pager", "--output", "short-iso"], None, 15).await,
        "apps" => cmd("journalctl", &["-u", "docker-compose@apps", "-n", &lines_s, "--no-pager", "--output", "short-iso"], None, 15).await,
        "edge" => cmd("journalctl", &["-u", "docker-compose@edge", "-n", &lines_s, "--no-pager", "--output", "short-iso"], None, 15).await,
        "ark" => cmd("journalctl", &["-u", "docker-compose@ark", "-n", &lines_s, "--no-pager", "--output", "short-iso"], None, 15).await,
        "dns" => cmd("journalctl", &["-u", "docker-compose@dns", "-n", &lines_s, "--no-pager", "--output", "short-iso"], None, 15).await,
        "home-finder" => cmd("journalctl", &["-u", "docker-compose@home-finder", "-n", &lines_s, "--no-pager", "--output", "short-iso"], None, 15).await,
        "wol" => cmd("journalctl", &["-u", "docker-compose@wol", "-n", &lines_s, "--no-pager", "--output", "short-iso"], None, 15).await,
        "dashboard-agent" => cmd("journalctl", &["-u", "dashboard-agent", "-n", &lines_s, "--no-pager", "--output", "short-iso"], None, 15).await,
        "apps-web-1" => cmd("docker", &["logs", "--tail", &lines_s, "apps-web-1"], None, 15).await,
        "apps-server-1" => cmd("docker", &["logs", "--tail", &lines_s, "apps-server-1"], None, 15).await,
        "apps-sports-api-1" => cmd("docker", &["logs", "--tail", &lines_s, "apps-sports-api-1"], None, 15).await,
        "apps-postgres-1" => cmd("docker", &["logs", "--tail", &lines_s, "apps-postgres-1"], None, 15).await,
        "apps-meilisearch-1" => cmd("docker", &["logs", "--tail", &lines_s, "apps-meilisearch-1"], None, 15).await,
        "apps-gluetun-1" => cmd("docker", &["logs", "--tail", &lines_s, "apps-gluetun-1"], None, 15).await,
        "dashboard" | "oliver-dashboard" => cmd("docker", &["logs", "--tail", &lines_s, "oliver-dashboard"], None, 15).await,
        "home-finder-app-1" => cmd("docker", &["logs", "--tail", &lines_s, "home-finder-app-1"], None, 15).await,
        "dns-adguardhome-1" => cmd("docker", &["logs", "--tail", &lines_s, "dns-adguardhome-1"], None, 15).await,
        "wol-wol-http-1" => cmd("docker", &["logs", "--tail", &lines_s, "wol-wol-http-1"], None, 15).await,
        "caddy" | "edge-caddy-1" => cmd("docker", &["logs", "--tail", &lines_s, "edge-caddy-1"], None, 15).await,
        "edge-cloudflare-ddns-1" => cmd("docker", &["logs", "--tail", &lines_s, "edge-cloudflare-ddns-1"], None, 15).await,
        "edge-cloudflare-ddns-ark-1" => cmd("docker", &["logs", "--tail", &lines_s, "edge-cloudflare-ddns-ark-1"], None, 15).await,
        "edge-cloudflare-ddns-dot-1" => cmd("docker", &["logs", "--tail", &lines_s, "edge-cloudflare-ddns-dot-1"], None, 15).await,
        _ => return Err(AgentError::NotFound)
    };
    Ok(Json(json!({"source": source, "stdout": strip_ansi(&res.stdout), "stderr": strip_ansi(&res.stderr), "exit_code": res.exit_code})))
}
async fn run_action(State(st): State<AgentState>, headers: HeaderMap, Path(id): Path<String>, body: Option<Json<ActionBody>>) -> Result<Json<Value>, AgentError> {
    auth(&st, &headers)?;
    let params = body.and_then(|Json(b)| b.params).unwrap_or(JsonValue::Null);
    let res = match id.as_str() {
        "service.reload.caddy" => cmd("docker", &["exec", "edge-caddy-1", "caddy", "reload", "--config", "/etc/caddy/Caddyfile"], None, 30).await,
        "service.start.tailscaled" => systemctl("start", "tailscaled", 30).await,
        "service.stop.tailscaled" => systemctl("stop", "tailscaled", 30).await,
        "service.restart.tailscaled" => systemctl("restart", "tailscaled", 30).await,
        "service.start.docker" => systemctl("start", "docker", 60).await,
        "service.stop.docker" => systemctl("stop", "docker", 60).await,
        "service.restart.docker" => systemctl("restart", "docker", 120).await,
        "service.start.edge" => systemctl("start", "docker-compose@edge", 60).await,
        "service.stop.edge" => systemctl("stop", "docker-compose@edge", 60).await,
        "service.restart.edge" => systemctl("restart", "docker-compose@edge", 60).await,
        "service.start.ark" => systemctl("start", "docker-compose@ark", 120).await,
        "service.stop.ark" => systemctl("stop", "docker-compose@ark", 120).await,
        "service.restart.ark" => systemctl("restart", "docker-compose@ark", 120).await,
        "service.start.dashboard-agent" => systemctl("start", "dashboard-agent", 30).await,
        "service.stop.dashboard-agent" => systemctl("stop", "dashboard-agent", 30).await,
        "service.restart.dashboard-agent" => systemctl("restart", "dashboard-agent", 30).await,
        "service.start.caddy" => docker_container("start", "edge-caddy-1", 30).await,
        "service.stop.caddy" => docker_container("stop", "edge-caddy-1", 30).await,
        "service.restart.caddy" => docker_container("restart", "edge-caddy-1", 30).await,
        "service.start.dashboard" => docker_container("start", "oliver-dashboard", 30).await,
        "service.stop.dashboard" => docker_container("stop", "oliver-dashboard", 30).await,
        "service.restart.dashboard" => docker_container("restart", "oliver-dashboard", 30).await,
        "service.start.apps" => systemctl("start", "docker-compose@apps", 60).await,
        "service.stop.apps" => systemctl("stop", "docker-compose@apps", 60).await,
        "service.restart.apps" => systemctl("restart", "docker-compose@apps", 60).await,
        "service.start.dns" => systemctl("start", "docker-compose@dns", 60).await,
        "service.stop.dns" => systemctl("stop", "docker-compose@dns", 60).await,
        "service.restart.dns" => systemctl("restart", "docker-compose@dns", 60).await,
        "service.start.home-finder" => systemctl("start", "docker-compose@home-finder", 60).await,
        "service.stop.home-finder" => systemctl("stop", "docker-compose@home-finder", 60).await,
        "service.restart.home-finder" => systemctl("restart", "docker-compose@home-finder", 60).await,
        "service.start.wol" => systemctl("start", "docker-compose@wol", 60).await,
        "service.stop.wol" => systemctl("stop", "docker-compose@wol", 60).await,
        "service.restart.wol" => systemctl("restart", "docker-compose@wol", 60).await,
        "service.start.apps-web-1" => docker_container("start", "apps-web-1", 30).await,
        "service.stop.apps-web-1" => docker_container("stop", "apps-web-1", 30).await,
        "service.restart.apps-web-1" => docker_container("restart", "apps-web-1", 30).await,
        "service.start.apps-server-1" => docker_container("start", "apps-server-1", 30).await,
        "service.stop.apps-server-1" => docker_container("stop", "apps-server-1", 30).await,
        "service.restart.apps-server-1" => docker_container("restart", "apps-server-1", 30).await,
        "service.start.apps-sports-api-1" => docker_container("start", "apps-sports-api-1", 30).await,
        "service.stop.apps-sports-api-1" => docker_container("stop", "apps-sports-api-1", 30).await,
        "service.restart.apps-sports-api-1" => docker_container("restart", "apps-sports-api-1", 30).await,
        "service.start.apps-postgres-1" => docker_container("start", "apps-postgres-1", 30).await,
        "service.stop.apps-postgres-1" => docker_container("stop", "apps-postgres-1", 30).await,
        "service.restart.apps-postgres-1" => docker_container("restart", "apps-postgres-1", 30).await,
        "service.start.apps-meilisearch-1" => docker_container("start", "apps-meilisearch-1", 30).await,
        "service.stop.apps-meilisearch-1" => docker_container("stop", "apps-meilisearch-1", 30).await,
        "service.restart.apps-meilisearch-1" => docker_container("restart", "apps-meilisearch-1", 30).await,
        "service.start.apps-gluetun-1" => docker_container("start", "apps-gluetun-1", 30).await,
        "service.stop.apps-gluetun-1" => docker_container("stop", "apps-gluetun-1", 30).await,
        "service.restart.apps-gluetun-1" => docker_container("restart", "apps-gluetun-1", 30).await,
        "service.start.oliver-dashboard" => docker_container("start", "oliver-dashboard", 30).await,
        "service.stop.oliver-dashboard" => docker_container("stop", "oliver-dashboard", 30).await,
        "service.restart.oliver-dashboard" => docker_container("restart", "oliver-dashboard", 30).await,
        "service.start.home-finder-app-1" => docker_container("start", "home-finder-app-1", 30).await,
        "service.stop.home-finder-app-1" => docker_container("stop", "home-finder-app-1", 30).await,
        "service.restart.home-finder-app-1" => docker_container("restart", "home-finder-app-1", 30).await,
        "service.start.dns-adguardhome-1" => docker_container("start", "dns-adguardhome-1", 30).await,
        "service.stop.dns-adguardhome-1" => docker_container("stop", "dns-adguardhome-1", 30).await,
        "service.restart.dns-adguardhome-1" => docker_container("restart", "dns-adguardhome-1", 30).await,
        "service.start.wol-wol-http-1" => docker_container("start", "wol-wol-http-1", 30).await,
        "service.stop.wol-wol-http-1" => docker_container("stop", "wol-wol-http-1", 30).await,
        "service.restart.wol-wol-http-1" => docker_container("restart", "wol-wol-http-1", 30).await,
        "wol.wake.pc" => wol_wake().await,
        "service.start.edge-caddy-1" => docker_container("start", "edge-caddy-1", 30).await,
        "service.stop.edge-caddy-1" => docker_container("stop", "edge-caddy-1", 30).await,
        "service.restart.edge-caddy-1" => docker_container("restart", "edge-caddy-1", 30).await,
        "service.start.edge-cloudflare-ddns-1" => docker_container("start", "edge-cloudflare-ddns-1", 30).await,
        "service.stop.edge-cloudflare-ddns-1" => docker_container("stop", "edge-cloudflare-ddns-1", 30).await,
        "service.restart.edge-cloudflare-ddns-1" => docker_container("restart", "edge-cloudflare-ddns-1", 30).await,
        "service.start.edge-cloudflare-ddns-ark-1" => docker_container("start", "edge-cloudflare-ddns-ark-1", 30).await,
        "service.stop.edge-cloudflare-ddns-ark-1" => docker_container("stop", "edge-cloudflare-ddns-ark-1", 30).await,
        "service.restart.edge-cloudflare-ddns-ark-1" => docker_container("restart", "edge-cloudflare-ddns-ark-1", 30).await,
        "service.start.edge-cloudflare-ddns-dot-1" => docker_container("start", "edge-cloudflare-ddns-dot-1", 30).await,
        "service.stop.edge-cloudflare-ddns-dot-1" => docker_container("stop", "edge-cloudflare-ddns-dot-1", 30).await,
        "service.restart.edge-cloudflare-ddns-dot-1" => docker_container("restart", "edge-cloudflare-ddns-dot-1", 30).await,
        "ark.status" => arkctl(&["status"], None, 30).await,
        "ark.players" => arkctl(&["players"], None, 30).await,
        "ark.config" => arkctl(&["config"], None, 30).await,
        "ark.info" => arkctl(&["info"], None, 30).await,
        "ark.connect" => arkctl(&["connect"], None, 30).await,
        "ark.logs" => { let tail = param_u64(&params, "tail").unwrap_or(120).min(1000).to_string(); arkctl(&["logs", "--tail", &tail], None, 30).await },
        "ark.rcon" => { let command = required_param(&params, "command")?; arkctl(&["rcon", &command], None, 60).await },
        "ark.say" => { let message = required_param(&params, "message")?; arkctl(&["say", &message], None, 60).await },
        "ark.start" => arkctl(&["start"], None, 120).await,
        "ark.stop" => arkctl(&["stop"], Some("stop\n"), 120).await,
        "ark.restart" => arkctl(&["restart"], Some("restart\n"), 180).await,
        "ark.saveworld" => arkctl(&["saveworld"], None, 60).await,
        "ark.backup" => arkctl(&["backup"], None, 300).await,
        "ark.update" => arkctl(&["update"], Some("update\n"), 900).await,
        "ark.recreate" => arkctl(&["recreate"], Some("recreate\n"), 300).await,
        "ark.resetworld" => {
            let mut args = vec!["reset-world"];
            if param_bool(&params, "no_backup") { args.push("--no-backup"); }
            if param_bool(&params, "no_restart") { args.push("--no-restart"); }
            arkctl(&args, Some("reset-world\n"), 300).await
        },
        "ark.destroywilddinos" => cmd("docker", &["exec", "ark-ase-island", "arkmanager", "rconcmd", "DestroyWildDinos"], None, 60).await,
        _ => return Err(AgentError::NotFound),
    };
    Ok(Json(json!({"action_id": id, "status": if res.exit_code == 0 {"succeeded"} else {"failed"}, "exit_code": res.exit_code, "stdout": strip_ansi(&res.stdout), "stderr": strip_ansi(&res.stderr)})))
}

async fn systemctl(action: &str, unit: &str, timeout_secs: u64) -> CmdOut { cmd("systemctl", &[action, unit], None, timeout_secs).await }
async fn docker_container(action: &str, name: &str, timeout_secs: u64) -> CmdOut { cmd("docker", &[action, name], None, timeout_secs).await }
async fn wol_wake() -> CmdOut {
    let url = std::env::var("WOL_HTTP_URL").unwrap_or_else(|_| "http://127.0.0.1:8765/wake".into());
    let Some(token) = read_secret("WOL_AUTH_TOKEN", "WOL_AUTH_TOKEN_FILE").or_else(|| read_dotenv_secret("/srv/compose/wol/.env", "WOL_AUTH_TOKEN")) else {
        return CmdOut { exit_code: -1, stdout: String::new(), stderr: "WOL_AUTH_TOKEN not configured".into() };
    };
    match timeout(Duration::from_secs(15), reqwest::Client::new().post(&url).bearer_auth(token).send()).await {
        Ok(Ok(res)) => {
            let status = res.status();
            let text = res.text().await.unwrap_or_default();
            CmdOut { exit_code: if status.is_success() { 0 } else { status.as_u16() as i32 }, stdout: text, stderr: if status.is_success() { String::new() } else { format!("WOL endpoint returned {status}") } }
        }
        Ok(Err(e)) => CmdOut { exit_code: -1, stdout: String::new(), stderr: e.to_string() },
        Err(_) => CmdOut { exit_code: -1, stdout: String::new(), stderr: "timed out after 15s".into() },
    }
}
async fn arkctl(args: &[&str], stdin: Option<&str>, timeout_secs: u64) -> CmdOut { cmd_input("./arkctl.py", args, Some("/srv/compose/ark"), stdin, timeout_secs).await }
fn required_param(params: &JsonValue, key: &str) -> Result<String, AgentError> { params.get(key).and_then(JsonValue::as_str).map(str::trim).filter(|s| !s.is_empty()).map(str::to_string).ok_or(AgentError::BadRequest) }
fn param_bool(params: &JsonValue, key: &str) -> bool { params.get(key).and_then(JsonValue::as_bool).unwrap_or(false) }
fn param_u64(params: &JsonValue, key: &str) -> Option<u64> { params.get(key).and_then(JsonValue::as_u64) }

fn auth(st: &AgentState, headers: &HeaderMap) -> Result<(), AgentError> { if let Some(expected)=&st.token { let got=headers.get("authorization").and_then(|v|v.to_str().ok()).unwrap_or(""); if got != format!("Bearer {}", expected.as_str()) { return Err(AgentError::Unauthorized); } } Ok(()) }
async fn ark_status() -> Value { let ctl = cmd("./arkctl.py", &["status"], Some("/srv/compose/ark"), 15).await; json!({"container": container_status("ark-ase-island").await, "exit_code": ctl.exit_code, "output": strip_ansi(&ctl.stdout)}) }
async fn container_status(name: &str) -> String { cmd("docker", &["inspect", "-f", "{{.State.Status}}", name], None, 5).await.stdout.trim().to_string() }
#[derive(Debug)] struct CmdOut { exit_code: i32, stdout: String, stderr: String }
async fn cmd(program: &str, args: &[&str], cwd: Option<&str>, timeout_secs: u64) -> CmdOut { cmd_input(program, args, cwd, None, timeout_secs).await }
async fn cmd_input(program: &str, args: &[&str], cwd: Option<&str>, stdin: Option<&str>, timeout_secs: u64) -> CmdOut { let mut c = Command::new(program); c.args(args).stdout(Stdio::piped()).stderr(Stdio::piped()); if stdin.is_some() { c.stdin(Stdio::piped()); } if let Some(cwd) = cwd { c.current_dir(cwd); } match timeout(Duration::from_secs(timeout_secs), async move { let mut child = c.spawn()?; if let Some(input) = stdin { if let Some(mut pipe) = child.stdin.take() { use tokio::io::AsyncWriteExt; pipe.write_all(input.as_bytes()).await?; } } child.wait_with_output().await }).await { Ok(Ok(o)) => CmdOut { exit_code: o.status.code().unwrap_or(-1), stdout: String::from_utf8_lossy(&o.stdout).to_string(), stderr: String::from_utf8_lossy(&o.stderr).to_string() }, Ok(Err(e)) => CmdOut { exit_code: -1, stdout: String::new(), stderr: e.to_string() }, Err(_) => CmdOut { exit_code: -1, stdout: String::new(), stderr: format!("timed out after {timeout_secs}s") } } }
fn parse_mem(mem: &str) -> Value { let mut total=0; let mut available=0; for line in mem.lines() { if line.starts_with("MemTotal:") { total=line.split_whitespace().nth(1).and_then(|v|v.parse::<u64>().ok()).unwrap_or(0); } if line.starts_with("MemAvailable:") { available=line.split_whitespace().nth(1).and_then(|v|v.parse::<u64>().ok()).unwrap_or(0); } } json!({"total_kb": total, "available_kb": available, "used_percent": if total>0 {(total-available)*100/total} else {0}}) }
fn parse_df(df: &str) -> Value { let line=df.lines().nth(1).unwrap_or(""); let p: Vec<_>=line.split_whitespace().collect(); json!({"size":p.get(1).unwrap_or(&""),"used":p.get(2).unwrap_or(&""),"available":p.get(3).unwrap_or(&""),"used_percent":p.get(4).unwrap_or(&"")}) }
fn strip_ansi(s: &str) -> String { let mut out=String::with_capacity(s.len()); let mut it=s.chars().peekable(); while let Some(c)=it.next() { if c=='\u{1b}' && it.peek()==Some(&'[') { it.next(); for ch in it.by_ref() { if ch.is_ascii_alphabetic() { break; } } } else { out.push(c); } } out }
fn read_secret(env: &str, file_env: &str) -> Option<String> { std::env::var(env).ok().or_else(|| std::env::var(file_env).ok().and_then(|p| std::fs::read_to_string(p).ok())).map(|s| s.trim().to_string()).filter(|s| !s.is_empty()) }
fn read_dotenv_secret(path: &str, key: &str) -> Option<String> { std::fs::read_to_string(path).ok()?.lines().find_map(|line| { let line=line.trim(); let (k,v)=line.split_once('=')?; (k.trim()==key).then(|| v.trim().trim_matches('"').trim_matches('\'').to_string()) }).filter(|s| !s.is_empty()) }
#[derive(Debug)] enum AgentError { Unauthorized, NotFound, BadRequest }
impl IntoResponse for AgentError { fn into_response(self) -> axum::response::Response { match self { AgentError::Unauthorized => (StatusCode::UNAUTHORIZED, Json(json!({"error":"unauthorized"}))).into_response(), AgentError::NotFound => (StatusCode::NOT_FOUND, Json(json!({"error":"not_found"}))).into_response(), AgentError::BadRequest => (StatusCode::BAD_REQUEST, Json(json!({"error":"bad_request"}))).into_response() } } }

#[cfg(test)]
mod tests { use super::*; #[test] fn strips_ansi_sequences() { assert_eq!(strip_ansi("a\u{1b}[31mred\u{1b}[0m"), "ared"); } #[test] fn auth_rejects_bad_token() { let st=AgentState{token:Some(Arc::new("secret".into()))}; assert!(matches!(auth(&st, &HeaderMap::new()), Err(AgentError::Unauthorized))); } }
