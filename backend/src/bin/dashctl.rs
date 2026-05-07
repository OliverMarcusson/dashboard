use anyhow::Context;
use chrono::{Duration, Utc};
use clap::{Parser, Subcommand};
use rand::{seq::SliceRandom, Rng};
use sha2::{Digest, Sha256};
use sqlx::sqlite::SqlitePoolOptions;
use uuid::Uuid;

#[derive(Parser)]
struct Cli {
    #[arg(long, env = "DASHBOARD_DATABASE_URL", default_value = "sqlite://dashboard.sqlite?mode=rwc")]
    database_url: String,
    #[command(subcommand)]
    command: Command,
}
#[derive(Subcommand)]
enum Command { Enroll { #[arg(long, default_value_t = 10)] ttl_minutes: i64 }, ListPasskeys }

#[tokio::main]
async fn main() -> anyhow::Result<()> {
    let cli = Cli::parse();
    let db = SqlitePoolOptions::new().max_connections(1).connect(&cli.database_url).await?;
    sqlx::migrate!("./migrations").run(&db).await?;
    match cli.command {
        Command::Enroll { ttl_minutes } => {
            let code = code();
            let hash = hex::encode(Sha256::digest(code.as_bytes()));
            let exp = (Utc::now() + Duration::minutes(ttl_minutes)).to_rfc3339();
            sqlx::query("INSERT INTO enrollment_codes(id,code_hash,expires_at) VALUES(?,?,?)")
                .bind(Uuid::new_v4().to_string()).bind(hash).bind(exp).execute(&db).await?;
            println!("Enrollment code:\n\n  {}\n\nOpen:\n\n  https://dash.marcusson.dev/enroll\n\nExpires in {} minutes.", code, ttl_minutes);
        }
        Command::ListPasskeys => {
            let rows: Vec<(String,String,String,Option<String>)> = sqlx::query_as("SELECT id,name,created_at,last_used_at FROM passkeys ORDER BY created_at")
                .fetch_all(&db).await.context("query passkeys")?;
            for (id,name,created,last) in rows { println!("{}\t{}\t{}\t{}", id, name, created, last.unwrap_or_else(|| "never".into())); }
        }
    }
    Ok(())
}

fn code() -> String {
    const WORDS: &[&str] = &["amber","atlas","brisk","cedar","copper","delta","ember","forest","glacier","harbor","ivory","juno","kepler","linen","meteor","north","onyx","prairie","quartz","river","solace","tundra","umbra","velvet","willow","xenon","yarrow","zenith"];
    let mut rng = rand::thread_rng();
    let n: u16 = rng.gen_range(1000..9999);
    format!("{}-{}-{}-{}-{}", WORDS.choose(&mut rng).unwrap(), WORDS.choose(&mut rng).unwrap(), n, WORDS.choose(&mut rng).unwrap(), WORDS.choose(&mut rng).unwrap())
}
