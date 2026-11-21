fn main() -> std::io::Result<()> {
    // Keep the synchronous PostgreSQL client outside Tokio's runtime lifecycle.
    let mut engine = rfq_dvp_market_engine::demo_engine();
    if let Ok(url) = std::env::var("DATABASE_URL") {
        let identity = std::env::var("MARKET_ID").unwrap_or_default();
        engine = engine.persist(&url, &identity).map_err(|_| {
            std::io::Error::other(
                "PostgreSQL unavailable, incompatible market identity or engine already running",
            )
        })?;
    }
    let engine = std::sync::Arc::new(engine);
    let runtime = tokio::runtime::Builder::new_multi_thread()
        .enable_all()
        .build()?;
    runtime.block_on(async {
        let listener = tokio::net::TcpListener::bind(
            std::env::var("ENGINE_ADDR").unwrap_or_else(|_| "127.0.0.1:8081".into()),
        )
        .await?;
        eprintln!(
            "rfq-dvp-market market engine : http://{}",
            listener.local_addr()?
        );
        axum::serve(
            listener,
            rfq_dvp_market_engine::router_with_engine(engine.clone()),
        )
        .with_graceful_shutdown(async {
            let _ = tokio::signal::ctrl_c().await;
        })
        .await
    })
}
