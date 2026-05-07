use axum::{extract::{Path, Query, State}, http::StatusCode, response::IntoResponse, routing::{get, patch, post}, Json, Router};
use axum_extra::extract::cookie::{Cookie, CookieJar, SameSite};
use chrono::{Duration, Utc};
use serde::{Deserialize, Serialize};
use serde_json::{json, Value};
use sha2::{Digest, Sha256};
use sqlx::{sqlite::SqlitePoolOptions, SqlitePool};
use std::{net::SocketAddr, sync::Arc};
use tower_http::{services::{ServeDir, ServeFile}, trace::TraceLayer};
use url::Url;
use uuid::Uuid;
use webauthn_rs::prelude::*;

const USER_ID: &str = "00000000-0000-0000-0000-000000000001";
const MAX_OUTPUT: usize = 256 * 1024;

#[derive(Clone)]
struct AppState { db: SqlitePool, webauthn: Arc<Webauthn>, agent_url: String, http: reqwest::Client, agent_token: Option<String> }

#[derive(Clone, Copy, Serialize)]
struct ActionSpec { id: &'static str, label: &'static str, danger: &'static str, confirmation: Option<&'static str> }
const ACTIONS: &[ActionSpec] = &[
    ActionSpec { id:"service.reload.caddy", label:"Reload Caddy", danger:"normal", confirmation:Some("reload caddy") },
    ActionSpec { id:"service.start.tailscaled", label:"Start tailscaled", danger:"normal", confirmation:None },
    ActionSpec { id:"service.stop.tailscaled", label:"Stop tailscaled", danger:"high", confirmation:Some("stop tailscaled") },
    ActionSpec { id:"service.restart.tailscaled", label:"Restart tailscaled", danger:"high", confirmation:Some("restart tailscaled") },
    ActionSpec { id:"service.start.docker", label:"Start Docker", danger:"normal", confirmation:None },
    ActionSpec { id:"service.stop.docker", label:"Stop Docker", danger:"high", confirmation:Some("stop docker") },
    ActionSpec { id:"service.restart.docker", label:"Restart Docker", danger:"high", confirmation:Some("restart docker") },
    ActionSpec { id:"service.start.edge", label:"Start edge stack", danger:"normal", confirmation:None },
    ActionSpec { id:"service.stop.edge", label:"Stop edge stack", danger:"high", confirmation:Some("stop edge") },
    ActionSpec { id:"service.restart.edge", label:"Restart edge stack", danger:"high", confirmation:Some("restart edge") },
    ActionSpec { id:"service.start.ark", label:"Start ARK stack", danger:"normal", confirmation:None },
    ActionSpec { id:"service.stop.ark", label:"Stop ARK stack", danger:"high", confirmation:Some("stop ark") },
    ActionSpec { id:"service.restart.ark", label:"Restart ARK stack", danger:"high", confirmation:Some("restart ark") },
    ActionSpec { id:"service.start.dashboard-agent", label:"Start dashboard agent", danger:"normal", confirmation:None },
    ActionSpec { id:"service.stop.dashboard-agent", label:"Stop dashboard agent", danger:"high", confirmation:Some("stop dashboard agent") },
    ActionSpec { id:"service.restart.dashboard-agent", label:"Restart dashboard agent", danger:"high", confirmation:Some("restart dashboard agent") },
    ActionSpec { id:"service.start.caddy", label:"Start Caddy container", danger:"normal", confirmation:None },
    ActionSpec { id:"service.stop.caddy", label:"Stop Caddy container", danger:"high", confirmation:Some("stop caddy") },
    ActionSpec { id:"service.restart.caddy", label:"Restart Caddy container", danger:"high", confirmation:Some("restart caddy") },
    ActionSpec { id:"service.start.dashboard", label:"Start dashboard container", danger:"normal", confirmation:None },
    ActionSpec { id:"service.stop.dashboard", label:"Stop dashboard container", danger:"high", confirmation:Some("stop dashboard") },
    ActionSpec { id:"service.restart.dashboard", label:"Restart dashboard container", danger:"high", confirmation:Some("restart dashboard") },
    ActionSpec { id:"service.start.apps", label:"Start Euripus stack", danger:"normal", confirmation:None },
    ActionSpec { id:"service.stop.apps", label:"Stop Euripus stack", danger:"high", confirmation:Some("stop apps") },
    ActionSpec { id:"service.restart.apps", label:"Restart Euripus stack", danger:"high", confirmation:Some("restart apps") },
    ActionSpec { id:"service.start.dns", label:"Start AdGuard Home DNS stack", danger:"normal", confirmation:None },
    ActionSpec { id:"service.stop.dns", label:"Stop AdGuard Home DNS stack", danger:"high", confirmation:Some("stop dns") },
    ActionSpec { id:"service.restart.dns", label:"Restart AdGuard Home DNS stack", danger:"high", confirmation:Some("restart dns") },
    ActionSpec { id:"service.start.home-finder", label:"Start Home Finder stack", danger:"normal", confirmation:None },
    ActionSpec { id:"service.stop.home-finder", label:"Stop Home Finder stack", danger:"high", confirmation:Some("stop home finder") },
    ActionSpec { id:"service.restart.home-finder", label:"Restart Home Finder stack", danger:"high", confirmation:Some("restart home finder") },
    ActionSpec { id:"service.start.wol", label:"Start Wake-on-LAN stack", danger:"normal", confirmation:None },
    ActionSpec { id:"service.stop.wol", label:"Stop Wake-on-LAN stack", danger:"high", confirmation:Some("stop wol") },
    ActionSpec { id:"service.restart.wol", label:"Restart Wake-on-LAN stack", danger:"high", confirmation:Some("restart wol") },
    ActionSpec { id:"service.start.apps-web-1", label:"Start Euripus web", danger:"normal", confirmation:None },
    ActionSpec { id:"service.stop.apps-web-1", label:"Stop Euripus web", danger:"high", confirmation:Some("stop apps-web-1") },
    ActionSpec { id:"service.restart.apps-web-1", label:"Restart Euripus web", danger:"high", confirmation:Some("restart apps-web-1") },
    ActionSpec { id:"service.start.apps-server-1", label:"Start Euripus server", danger:"normal", confirmation:None },
    ActionSpec { id:"service.stop.apps-server-1", label:"Stop Euripus server", danger:"high", confirmation:Some("stop apps-server-1") },
    ActionSpec { id:"service.restart.apps-server-1", label:"Restart Euripus server", danger:"high", confirmation:Some("restart apps-server-1") },
    ActionSpec { id:"service.start.apps-sports-api-1", label:"Start Euripus sports API", danger:"normal", confirmation:None },
    ActionSpec { id:"service.stop.apps-sports-api-1", label:"Stop Euripus sports API", danger:"high", confirmation:Some("stop apps-sports-api-1") },
    ActionSpec { id:"service.restart.apps-sports-api-1", label:"Restart Euripus sports API", danger:"high", confirmation:Some("restart apps-sports-api-1") },
    ActionSpec { id:"service.start.apps-postgres-1", label:"Start Euripus Postgres", danger:"normal", confirmation:None },
    ActionSpec { id:"service.stop.apps-postgres-1", label:"Stop Euripus Postgres", danger:"high", confirmation:Some("stop apps-postgres-1") },
    ActionSpec { id:"service.restart.apps-postgres-1", label:"Restart Euripus Postgres", danger:"high", confirmation:Some("restart apps-postgres-1") },
    ActionSpec { id:"service.start.apps-meilisearch-1", label:"Start Euripus Meilisearch", danger:"normal", confirmation:None },
    ActionSpec { id:"service.stop.apps-meilisearch-1", label:"Stop Euripus Meilisearch", danger:"high", confirmation:Some("stop apps-meilisearch-1") },
    ActionSpec { id:"service.restart.apps-meilisearch-1", label:"Restart Euripus Meilisearch", danger:"high", confirmation:Some("restart apps-meilisearch-1") },
    ActionSpec { id:"service.start.apps-gluetun-1", label:"Start Euripus Gluetun", danger:"normal", confirmation:None },
    ActionSpec { id:"service.stop.apps-gluetun-1", label:"Stop Euripus Gluetun", danger:"high", confirmation:Some("stop apps-gluetun-1") },
    ActionSpec { id:"service.restart.apps-gluetun-1", label:"Restart Euripus Gluetun", danger:"high", confirmation:Some("restart apps-gluetun-1") },
    ActionSpec { id:"service.start.oliver-dashboard", label:"Start Oliver Dashboard", danger:"normal", confirmation:None },
    ActionSpec { id:"service.stop.oliver-dashboard", label:"Stop Oliver Dashboard", danger:"high", confirmation:Some("stop oliver-dashboard") },
    ActionSpec { id:"service.restart.oliver-dashboard", label:"Restart Oliver Dashboard", danger:"high", confirmation:Some("restart oliver-dashboard") },
    ActionSpec { id:"service.start.home-finder-app-1", label:"Start Home Finder", danger:"normal", confirmation:None },
    ActionSpec { id:"service.stop.home-finder-app-1", label:"Stop Home Finder", danger:"high", confirmation:Some("stop home-finder-app-1") },
    ActionSpec { id:"service.restart.home-finder-app-1", label:"Restart Home Finder", danger:"high", confirmation:Some("restart home-finder-app-1") },
    ActionSpec { id:"service.start.dns-adguardhome-1", label:"Start AdGuard Home DNS", danger:"normal", confirmation:None },
    ActionSpec { id:"service.stop.dns-adguardhome-1", label:"Stop AdGuard Home DNS", danger:"high", confirmation:Some("stop dns-adguardhome-1") },
    ActionSpec { id:"service.restart.dns-adguardhome-1", label:"Restart AdGuard Home DNS", danger:"high", confirmation:Some("restart dns-adguardhome-1") },
    ActionSpec { id:"service.start.wol-wol-http-1", label:"Start Wake-on-LAN HTTP", danger:"normal", confirmation:None },
    ActionSpec { id:"service.stop.wol-wol-http-1", label:"Stop Wake-on-LAN HTTP", danger:"high", confirmation:Some("stop wol-wol-http-1") },
    ActionSpec { id:"service.restart.wol-wol-http-1", label:"Restart Wake-on-LAN HTTP", danger:"high", confirmation:Some("restart wol-wol-http-1") },
    ActionSpec { id:"wol.wake.pc", label:"Wake PC", danger:"normal", confirmation:None },
    ActionSpec { id:"service.start.edge-caddy-1", label:"Start Caddy ingress", danger:"normal", confirmation:None },
    ActionSpec { id:"service.stop.edge-caddy-1", label:"Stop Caddy ingress", danger:"high", confirmation:Some("stop edge-caddy-1") },
    ActionSpec { id:"service.restart.edge-caddy-1", label:"Restart Caddy ingress", danger:"high", confirmation:Some("restart edge-caddy-1") },
    ActionSpec { id:"service.start.edge-cloudflare-ddns-1", label:"Start Cloudflare DDNS", danger:"normal", confirmation:None },
    ActionSpec { id:"service.stop.edge-cloudflare-ddns-1", label:"Stop Cloudflare DDNS", danger:"high", confirmation:Some("stop edge-cloudflare-ddns-1") },
    ActionSpec { id:"service.restart.edge-cloudflare-ddns-1", label:"Restart Cloudflare DDNS", danger:"high", confirmation:Some("restart edge-cloudflare-ddns-1") },
    ActionSpec { id:"service.start.edge-cloudflare-ddns-ark-1", label:"Start Cloudflare DDNS ARK", danger:"normal", confirmation:None },
    ActionSpec { id:"service.stop.edge-cloudflare-ddns-ark-1", label:"Stop Cloudflare DDNS ARK", danger:"high", confirmation:Some("stop edge-cloudflare-ddns-ark-1") },
    ActionSpec { id:"service.restart.edge-cloudflare-ddns-ark-1", label:"Restart Cloudflare DDNS ARK", danger:"high", confirmation:Some("restart edge-cloudflare-ddns-ark-1") },
    ActionSpec { id:"service.start.edge-cloudflare-ddns-dot-1", label:"Start Cloudflare DDNS DoT", danger:"normal", confirmation:None },
    ActionSpec { id:"service.stop.edge-cloudflare-ddns-dot-1", label:"Stop Cloudflare DDNS DoT", danger:"high", confirmation:Some("stop edge-cloudflare-ddns-dot-1") },
    ActionSpec { id:"service.restart.edge-cloudflare-ddns-dot-1", label:"Restart Cloudflare DDNS DoT", danger:"high", confirmation:Some("restart edge-cloudflare-ddns-dot-1") },
    ActionSpec { id:"ark.status", label:"ARK status", danger:"low", confirmation:None },
    ActionSpec { id:"ark.players", label:"List ARK players", danger:"low", confirmation:None },
    ActionSpec { id:"ark.config", label:"ARK config summary", danger:"low", confirmation:None },
    ActionSpec { id:"ark.info", label:"ARK server info", danger:"low", confirmation:None },
    ActionSpec { id:"ark.connect", label:"ARK connect commands", danger:"low", confirmation:None },
    ActionSpec { id:"ark.logs", label:"ARK recent logs", danger:"low", confirmation:None },
    ActionSpec { id:"ark.rcon", label:"Run ARK RCON command", danger:"high", confirmation:Some("run rcon") },
    ActionSpec { id:"ark.say", label:"Broadcast ARK message", danger:"normal", confirmation:None },
    ActionSpec { id:"ark.start", label:"Start ARK service", danger:"normal", confirmation:None },
    ActionSpec { id:"ark.stop", label:"Stop ARK service", danger:"high", confirmation:Some("stop ark") },
    ActionSpec { id:"ark.restart", label:"Restart ARK service", danger:"high", confirmation:Some("restart ark") },
    ActionSpec { id:"ark.saveworld", label:"Save ARK world", danger:"normal", confirmation:Some("save world") },
    ActionSpec { id:"ark.backup", label:"Run ARK backup", danger:"normal", confirmation:Some("backup ark") },
    ActionSpec { id:"ark.update", label:"Update ARK server", danger:"high", confirmation:Some("update ark") },
    ActionSpec { id:"ark.recreate", label:"Recreate ARK container", danger:"high", confirmation:Some("recreate ark") },
    ActionSpec { id:"ark.resetworld", label:"Reset ARK world save", danger:"destructive", confirmation:Some("reset-world") },
    ActionSpec { id:"ark.destroywilddinos", label:"Destroy wild dinos", danger:"destructive", confirmation:Some("destroy wild dinos") },
];

#[derive(Deserialize)] struct EnrollStart { code: String, device_name: String }
#[derive(Deserialize)] struct Finish<T> { state_id: String, credential: T, device_name: Option<String> }
#[derive(Serialize)] struct StartResponse<T> { state_id: String, public_key: T }
#[derive(Serialize)] struct SessionResponse { authenticated: bool, username: Option<String> }
#[derive(Deserialize)] struct ActionRun { confirmation: Option<String>, params: Option<Value> }
#[derive(Deserialize)] struct LogQuery { lines: Option<usize> }
#[derive(Deserialize)] struct RenamePasskey { name: String }

#[tokio::main]
async fn main() -> anyhow::Result<()> {
    tracing_subscriber::fmt().with_env_filter("info,tower_http=info").init();
    let db_url = std::env::var("DASHBOARD_DATABASE_URL").unwrap_or_else(|_| "sqlite://dashboard.sqlite?mode=rwc".into());
    let bind = std::env::var("DASHBOARD_BIND").unwrap_or_else(|_| "127.0.0.1:3000".into());
    let rp_id = std::env::var("DASHBOARD_RP_ID").unwrap_or_else(|_| "dash.marcusson.dev".into());
    let origin = std::env::var("DASHBOARD_ORIGIN").unwrap_or_else(|_| "https://dash.marcusson.dev".into());
    let pool = SqlitePoolOptions::new().max_connections(5).connect(&db_url).await?;
    sqlx::migrate!("./migrations").run(&pool).await?;
    backfill_credential_ids(&pool).await;
    let webauthn = WebauthnBuilder::new(&rp_id, &Url::parse(&origin)?)?.rp_name("Oliver Server Dashboard").build()?;
    let agent_url = std::env::var("DASHBOARD_AGENT_URL").unwrap_or_else(|_| "http://host.docker.internal:13001".into());
    let agent_token = read_secret("DASHBOARD_AGENT_TOKEN", "DASHBOARD_AGENT_TOKEN_FILE");
    let state = AppState { db: pool, webauthn: Arc::new(webauthn), agent_url, http: reqwest::Client::new(), agent_token };
    let app = Router::new()
        .route("/api/health", get(health))
        .route("/api/auth/session", get(session)).route("/api/auth/logout", post(logout))
        .route("/api/auth/enroll/start", post(enroll_start)).route("/api/auth/enroll/finish", post(enroll_finish))
        .route("/api/auth/login/start", post(login_start)).route("/api/auth/login/finish", post(login_finish))
        .route("/api/audit", get(audit_list)).route("/api/system/overview", get(system_overview)).route("/api/services", get(services_list))
        .route("/api/logs/:source", get(logs_get)).route("/api/actions", get(actions_list)).route("/api/actions/:id/run", post(action_run))
        .route("/api/jobs", get(jobs_list)).route("/api/jobs/:id", get(job_get))
        .route("/api/security/passkeys", get(passkeys_list)).route("/api/security/passkeys/:id", patch(passkey_rename).delete(passkey_revoke))
        .fallback_service(ServeDir::new("../frontend/dist").fallback(ServeFile::new("../frontend/dist/index.html")))
        .layer(TraceLayer::new_for_http()).with_state(state);
    let listener = tokio::net::TcpListener::bind(&bind).await?;
    tracing::info!(%bind, "dashboard listening");
    axum::serve(listener, app.into_make_service_with_connect_info::<SocketAddr>()).await?;
    Ok(())
}

async fn health() -> Json<Value> { Json(json!({"ok":true})) }

async fn enroll_start(State(st): State<AppState>, Json(req): Json<EnrollStart>) -> Result<Json<StartResponse<CreationChallengeResponse>>, AppError> {
    verify_code(&st.db, &req.code).await?;
    audit(&st.db, "enroll_started", json!({"device": req.device_name})).await;
    let (ccr, reg_state) = st.webauthn.start_passkey_registration(Uuid::parse_str(USER_ID)?, "oliver", "Oliver", None)?;
    Ok(Json(StartResponse { state_id: save_state(&st.db, "registration", &reg_state).await?, public_key: ccr }))
}
async fn enroll_finish(State(st): State<AppState>, jar: CookieJar, Json(req): Json<Finish<RegisterPublicKeyCredential>>) -> Result<impl IntoResponse, AppError> {
    let reg_state: PasskeyRegistration = take_state(&st.db, &req.state_id, "registration").await?;
    let passkey = st.webauthn.finish_passkey_registration(&req.credential, &reg_state)?;
    let credential_id = credential_id_from_passkey(&passkey);
    sqlx::query("INSERT OR IGNORE INTO users(id, username, display_name) VALUES(?,'oliver','Oliver')").bind(USER_ID).execute(&st.db).await?;
    sqlx::query("INSERT INTO passkeys(id,user_id,name,credential_json,credential_id) VALUES(?,?,?,?,?)")
        .bind(Uuid::new_v4().to_string()).bind(USER_ID).bind(req.device_name.unwrap_or_else(|| "Passkey".into())).bind(serde_json::to_string(&passkey)?).bind(credential_id).execute(&st.db).await?;
    audit(&st.db, "passkey_registered", json!({})).await;
    Ok((issue_session(&st.db, jar, USER_ID).await?, Json(json!({"ok": true}))))
}
async fn login_start(State(st): State<AppState>) -> Result<Json<StartResponse<RequestChallengeResponse>>, AppError> {
    let rows: Vec<(String,)> = sqlx::query_as("SELECT credential_json FROM passkeys WHERE revoked_at IS NULL").fetch_all(&st.db).await?;
    if rows.is_empty() { return Err(AppError::Unauthorized); }
    let passkeys: Vec<Passkey> = rows.into_iter().map(|r| serde_json::from_str(&r.0)).collect::<Result<_,_>>()?;
    let (rcr, auth_state) = st.webauthn.start_passkey_authentication(&passkeys)?;
    Ok(Json(StartResponse { state_id: save_state(&st.db, "authentication", &auth_state).await?, public_key: rcr }))
}
async fn login_finish(State(st): State<AppState>, jar: CookieJar, Json(req): Json<Finish<PublicKeyCredential>>) -> Result<impl IntoResponse, AppError> {
    let auth_state: PasskeyAuthentication = take_state(&st.db, &req.state_id, "authentication").await?;
    let auth_result = st.webauthn.finish_passkey_authentication(&req.credential, &auth_state)?;
    let credential_id = serde_json::to_string(auth_result.cred_id())?;
    let row: Option<(String,String)> = sqlx::query_as("SELECT id,user_id FROM passkeys WHERE credential_id=? AND revoked_at IS NULL").bind(&credential_id).fetch_optional(&st.db).await?;
    let (passkey_id, user_id) = row.ok_or(AppError::Unauthorized)?;
    sqlx::query("UPDATE passkeys SET last_used_at=CURRENT_TIMESTAMP WHERE id=?").bind(&passkey_id).execute(&st.db).await?;
    audit(&st.db, "login_success", json!({"passkey_id": passkey_id})).await;
    Ok((issue_session(&st.db, jar, &user_id).await?, Json(json!({"ok": true}))))
}
async fn session(State(st): State<AppState>, jar: CookieJar) -> Json<SessionResponse> {
    if let Some(sid) = jar.get("dashboard_session").map(|c| c.value().to_string()) {
        if let Ok(Some((username,))) = sqlx::query_as::<_, (String,)>("SELECT users.username FROM sessions JOIN users ON users.id=sessions.user_id WHERE sessions.id=? AND sessions.expires_at > datetime('now')").bind(sid).fetch_optional(&st.db).await { return Json(SessionResponse { authenticated: true, username: Some(username) }) }
    }
    Json(SessionResponse { authenticated: false, username: None })
}
async fn logout(State(st): State<AppState>, jar: CookieJar) -> impl IntoResponse { if let Some(c)=jar.get("dashboard_session") { let _=sqlx::query("DELETE FROM sessions WHERE id=?").bind(c.value()).execute(&st.db).await; } (jar.remove(Cookie::from("dashboard_session")), Json(json!({"ok":true}))) }

async fn audit_list(State(st): State<AppState>, jar: CookieJar) -> Result<Json<Vec<Value>>, AppError> { require_auth(&st.db, &jar).await?; let rows: Vec<(String,String,String)> = sqlx::query_as("SELECT created_at,event,detail_json FROM audit_events ORDER BY created_at DESC LIMIT 100").fetch_all(&st.db).await?; Ok(Json(rows.into_iter().map(|(created_at,event,detail_json)| json!({"created_at":created_at,"event":event,"detail":serde_json::from_str::<Value>(&detail_json).unwrap_or(json!({}))})).collect())) }
async fn system_overview(State(st): State<AppState>, jar: CookieJar) -> Result<Json<Value>, AppError> { require_auth(&st.db, &jar).await?; agent_get(&st, "/v1/overview").await }
async fn services_list(State(st): State<AppState>, jar: CookieJar) -> Result<Json<Value>, AppError> { require_auth(&st.db, &jar).await?; agent_get(&st, "/v1/services").await }
async fn logs_get(State(st): State<AppState>, jar: CookieJar, Path(source): Path<String>, Query(q): Query<LogQuery>) -> Result<Json<Value>, AppError> { require_auth(&st.db, &jar).await?; agent_get(&st, &format!("/v1/logs/{source}?lines={}", q.lines.unwrap_or(200).min(1000))).await }
async fn actions_list(State(st): State<AppState>, jar: CookieJar) -> Result<Json<Vec<ActionSpec>>, AppError> { require_auth(&st.db, &jar).await?; Ok(Json(ACTIONS.to_vec())) }

async fn action_run(State(st): State<AppState>, jar: CookieJar, Path(id): Path<String>, Json(req): Json<ActionRun>) -> Result<Json<Value>, AppError> {
    let user_id = require_auth(&st.db, &jar).await?;
    let spec = action_spec(&id).ok_or(AppError::NotFound)?;
    if let Some(required) = spec.confirmation { if req.confirmation.as_deref() != Some(required) { return Err(AppError::BadRequest(format!("confirmation must be: {required}"))); } }
    let job_id = Uuid::new_v4().to_string();
    sqlx::query("INSERT INTO jobs(id,action_id,status) VALUES(?,?,?)").bind(&job_id).bind(&id).bind("running").execute(&st.db).await?;
    audit(&st.db, "action_started", json!({"job_id": job_id, "action_id": id, "user_id": user_id})).await;
    let worker = st.clone(); let jid = job_id.clone(); let aid = id.clone(); let params = req.params.unwrap_or(Value::Null);
    tokio::spawn(async move { run_job(worker, jid, aid, params).await; });
    Ok(Json(json!({"job_id": job_id, "status":"running"})))
}
async fn run_job(st: AppState, job_id: String, action_id: String, params: Value) {
    let res = agent_post(&st, &format!("/v1/actions/{action_id}/run"), params).await;
    let (status, exit_code, stdout, stderr) = match res {
        Ok(v) => (v.get("status").and_then(Value::as_str).unwrap_or("failed").to_string(), v.get("exit_code").and_then(Value::as_i64).unwrap_or(-1), truncate(v.get("stdout").and_then(Value::as_str).unwrap_or("")), truncate(v.get("stderr").and_then(Value::as_str).unwrap_or(""))),
        Err(e) => ("failed".into(), -1, String::new(), truncate(&format!("{e:?}"))),
    };
    let _=sqlx::query("UPDATE jobs SET status=?, finished_at=CURRENT_TIMESTAMP, exit_code=?, stdout=?, stderr=? WHERE id=?").bind(&status).bind(exit_code).bind(stdout).bind(stderr).bind(&job_id).execute(&st.db).await;
    audit(&st.db, "action_finished", json!({"job_id": job_id, "action_id": action_id, "status": status})).await;
}
async fn jobs_list(State(st): State<AppState>, jar: CookieJar) -> Result<Json<Vec<Value>>, AppError> { require_auth(&st.db, &jar).await?; let rows: Vec<(String,String,String,String,Option<String>,Option<i64>,String,String)> = sqlx::query_as("SELECT id,action_id,status,started_at,finished_at,exit_code,stdout,stderr FROM jobs ORDER BY started_at DESC LIMIT 50").fetch_all(&st.db).await?; Ok(Json(rows.into_iter().map(job_json).collect())) }
async fn job_get(State(st): State<AppState>, jar: CookieJar, Path(id): Path<String>) -> Result<Json<Value>, AppError> { require_auth(&st.db, &jar).await?; let r: (String,String,String,String,Option<String>,Option<i64>,String,String) = sqlx::query_as("SELECT id,action_id,status,started_at,finished_at,exit_code,stdout,stderr FROM jobs WHERE id=?").bind(id).fetch_optional(&st.db).await?.ok_or(AppError::NotFound)?; Ok(Json(job_json(r))) }
fn job_json(r: (String,String,String,String,Option<String>,Option<i64>,String,String)) -> Value { json!({"id":r.0,"action_id":r.1,"status":r.2,"started_at":r.3,"finished_at":r.4,"exit_code":r.5,"stdout":r.6,"stderr":r.7}) }

async fn passkeys_list(State(st): State<AppState>, jar: CookieJar) -> Result<Json<Vec<Value>>, AppError> { require_auth(&st.db, &jar).await?; let rows: Vec<(String,String,String,Option<String>,Option<String>)> = sqlx::query_as("SELECT id,name,created_at,last_used_at,revoked_at FROM passkeys ORDER BY created_at DESC").fetch_all(&st.db).await?; Ok(Json(rows.into_iter().map(|r|json!({"id":r.0,"name":r.1,"created_at":r.2,"last_used_at":r.3,"revoked_at":r.4})).collect())) }
async fn passkey_rename(State(st): State<AppState>, jar: CookieJar, Path(id): Path<String>, Json(req): Json<RenamePasskey>) -> Result<Json<Value>, AppError> { require_auth(&st.db, &jar).await?; let name=req.name.trim(); if name.is_empty() || name.len()>80 { return Err(AppError::BadRequest("name must be 1-80 chars".into())); } sqlx::query("UPDATE passkeys SET name=? WHERE id=? AND revoked_at IS NULL").bind(name).bind(id).execute(&st.db).await?; Ok(Json(json!({"ok":true}))) }
async fn passkey_revoke(State(st): State<AppState>, jar: CookieJar, Path(id): Path<String>) -> Result<Json<Value>, AppError> { require_auth(&st.db, &jar).await?; let active: (i64,) = sqlx::query_as("SELECT COUNT(*) FROM passkeys WHERE revoked_at IS NULL").fetch_one(&st.db).await?; if active.0 <= 1 { return Err(AppError::BadRequest("cannot revoke the last active passkey; enroll another passkey first".into())); } sqlx::query("UPDATE passkeys SET revoked_at=CURRENT_TIMESTAMP WHERE id=? AND revoked_at IS NULL").bind(id).execute(&st.db).await?; audit(&st.db, "passkey_revoked", json!({})).await; Ok(Json(json!({"ok":true}))) }

async fn require_auth(db: &SqlitePool, jar: &CookieJar) -> Result<String, AppError> { let sid = jar.get("dashboard_session").map(|c| c.value().to_string()).ok_or(AppError::Unauthorized)?; let row: Option<(String,)> = sqlx::query_as("SELECT user_id FROM sessions WHERE id=? AND expires_at > datetime('now')").bind(sid).fetch_optional(db).await?; row.map(|r| r.0).ok_or(AppError::Unauthorized) }
async fn agent_get(st: &AppState, path: &str) -> Result<Json<Value>, AppError> { Ok(Json(agent_request(st.http.get(format!("{}{}", st.agent_url, path)), &st.agent_token).await?)) }
async fn agent_post(st: &AppState, path: &str, params: Value) -> Result<Value, AppError> { agent_request(st.http.post(format!("{}{}", st.agent_url, path)).json(&json!({"params": params})), &st.agent_token).await }
async fn agent_request(mut req: reqwest::RequestBuilder, token: &Option<String>) -> Result<Value, AppError> { if let Some(t)=token { req=req.bearer_auth(t); } Ok(req.send().await?.error_for_status()?.json().await?) }
fn action_spec(id: &str) -> Option<ActionSpec> { ACTIONS.iter().copied().find(|a| a.id==id) }
fn truncate(s: &str) -> String { if s.len() <= MAX_OUTPUT { s.to_string() } else { format!("{}\n...[truncated to {MAX_OUTPUT} bytes]", &s[..MAX_OUTPUT]) } }
fn credential_id_from_passkey(passkey: &Passkey) -> String { serde_json::to_string(passkey.cred_id()).unwrap_or_default() }
async fn issue_session(db: &SqlitePool, jar: CookieJar, user_id: &str) -> Result<CookieJar, AppError> { let sid=Uuid::new_v4().to_string(); let exp=(Utc::now()+Duration::days(14)).to_rfc3339(); sqlx::query("INSERT INTO sessions(id,user_id,expires_at) VALUES(?,?,?)").bind(&sid).bind(user_id).bind(exp).execute(db).await?; Ok(jar.add(Cookie::build(("dashboard_session", sid)).path("/").http_only(true).secure(true).same_site(SameSite::Strict).build())) }
async fn save_state<T: Serialize>(db: &SqlitePool, kind: &str, value: &T) -> Result<String, AppError> { let id=Uuid::new_v4().to_string(); let exp=(Utc::now()+Duration::minutes(5)).to_rfc3339(); sqlx::query("INSERT INTO webauthn_states(id,kind,state_json,expires_at) VALUES(?,?,?,?)").bind(&id).bind(kind).bind(serde_json::to_string(value)?).bind(exp).execute(db).await?; Ok(id) }
async fn take_state<T: for<'de> Deserialize<'de>>(db: &SqlitePool, id: &str, kind: &str) -> Result<T, AppError> { let row: (String,) = sqlx::query_as("SELECT state_json FROM webauthn_states WHERE id=? AND kind=? AND expires_at > datetime('now')").bind(id).bind(kind).fetch_one(db).await?; sqlx::query("DELETE FROM webauthn_states WHERE id=?").bind(id).execute(db).await?; Ok(serde_json::from_str(&row.0)?) }
async fn verify_code(db: &SqlitePool, code: &str) -> Result<(), AppError> { let hash=hash_code(code); let rows=sqlx::query("UPDATE enrollment_codes SET used_at=CURRENT_TIMESTAMP WHERE code_hash=? AND used_at IS NULL AND expires_at > datetime('now')").bind(hash).execute(db).await?; if rows.rows_affected()==1 { Ok(()) } else { Err(AppError::Unauthorized) } }
fn hash_code(code: &str) -> String { hex::encode(Sha256::digest(code.trim().as_bytes())) }
async fn audit(db: &SqlitePool, event: &str, detail: Value) { let _=sqlx::query("INSERT INTO audit_events(id,event,detail_json) VALUES(?,?,?)").bind(Uuid::new_v4().to_string()).bind(event).bind(detail.to_string()).execute(db).await; }
async fn backfill_credential_ids(db: &SqlitePool) { if let Ok(rows) = sqlx::query_as::<_,(String,String)>("SELECT id,credential_json FROM passkeys WHERE credential_id IS NULL").fetch_all(db).await { for (id,js) in rows { if let Ok(p)=serde_json::from_str::<Passkey>(&js) { let _=sqlx::query("UPDATE passkeys SET credential_id=? WHERE id=?").bind(credential_id_from_passkey(&p)).bind(id).execute(db).await; } } } }
fn read_secret(env: &str, file_env: &str) -> Option<String> { std::env::var(env).ok().or_else(|| std::env::var(file_env).ok().and_then(|p| std::fs::read_to_string(p).ok())).map(|s| s.trim().to_string()).filter(|s| !s.is_empty()) }

#[derive(Debug)] enum AppError { Unauthorized, BadRequest(String), NotFound, Any(anyhow::Error) }
impl<E> From<E> for AppError where E: Into<anyhow::Error> { fn from(e: E) -> Self { Self::Any(e.into()) } }
impl IntoResponse for AppError { fn into_response(self) -> axum::response::Response { match self { AppError::Unauthorized => (StatusCode::UNAUTHORIZED, Json(json!({"error":"unauthorized"}))).into_response(), AppError::BadRequest(message) => (StatusCode::BAD_REQUEST, Json(json!({"error":"bad_request","message":message}))).into_response(), AppError::NotFound => (StatusCode::NOT_FOUND, Json(json!({"error":"not_found"}))).into_response(), AppError::Any(e) => { tracing::error!(error=?e); (StatusCode::INTERNAL_SERVER_ERROR, Json(json!({"error":"internal_error"}))).into_response() } } } }

#[cfg(test)]
mod tests { use super::*; #[test] fn action_registry_rejects_unknown() { assert!(action_spec("ark.status").is_some()); assert!(action_spec("shell.exec").is_none()); } #[test] fn output_is_truncated() { let s="x".repeat(MAX_OUTPUT+10); let t=truncate(&s); assert!(t.len() < s.len()+100); assert!(t.contains("truncated")); } #[test] fn hashes_trimmed_codes() { assert_eq!(hash_code(" abc "), hash_code("abc")); } }
