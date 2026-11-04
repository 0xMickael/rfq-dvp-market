#[tokio::main]
async fn main() -> std::io::Result<()> {
    let listener = tokio::net::TcpListener::bind(
        std::env::var("ENGINE_ADDR").unwrap_or_else(|_| "127.0.0.1:8081".into()),
    )
    .await?;
    eprintln!(
        "rfq-dvp-market market engine : http://{}",
        listener.local_addr()?
    );

    axum::serve(listener, rfq_dvp_market_engine::router())
        .with_graceful_shutdown(async {
            if let Err(error) = tokio::signal::ctrl_c().await {
                eprintln!("Impossible d'écouter Ctrl+C : {error}");
            }
        })
        .await
}
