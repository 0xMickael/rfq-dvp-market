//! Bounded route/status labels; no RFQ, quote or wallet identifiers in metrics.
use axum::{
    body::Body,
    extract::{MatchedPath, State},
    http::{Request, header},
    middleware::Next,
    response::Response,
};
use std::{
    collections::BTreeMap,
    sync::{Arc, Mutex},
    time::Instant,
};

const BOUNDS: [f64; 6] = [0.005, 0.025, 0.1, 0.5, 2.0, 5.0];
#[derive(Default)]
struct Sample {
    count: u64,
    seconds: f64,
    buckets: [u64; 6],
}
#[derive(Clone, Default)]
pub(crate) struct Metrics(Arc<Mutex<BTreeMap<(String, u16), Sample>>>);

pub(crate) async fn record(
    State(metrics): State<Metrics>,
    request: Request<Body>,
    next: Next,
) -> Response {
    let route = request
        .extensions()
        .get::<MatchedPath>()
        .map(|p| p.as_str())
        .unwrap_or("unmatched")
        .to_owned();
    if route == "/metrics" {
        return next.run(request).await;
    }
    let started = Instant::now();
    let response = next.run(request).await;
    let seconds = started.elapsed().as_secs_f64();
    if let Ok(mut samples) = metrics.0.lock() {
        let sample = samples
            .entry((route, response.status().as_u16() / 100))
            .or_default();
        sample.count += 1;
        sample.seconds += seconds;
        for (index, bound) in BOUNDS.iter().enumerate() {
            if seconds <= *bound {
                sample.buckets[index] += 1;
            }
        }
    }
    response
}
pub(crate) async fn expose(
    State(metrics): State<Metrics>,
) -> ([(header::HeaderName, &'static str); 1], String) {
    use std::fmt::Write;
    let mut output = String::from(
        "# TYPE rfq_engine_http_requests_total counter\n# TYPE rfq_engine_http_request_duration_seconds histogram\n",
    );
    if let Ok(samples) = metrics.0.lock() {
        for ((route, status), sample) in samples.iter() {
            let labels = format!("route={route:?},status=\"{status}xx\"");
            let _ = writeln!(
                output,
                "rfq_engine_http_requests_total{{{labels}}} {}",
                sample.count
            );
            for (index, bound) in BOUNDS.iter().enumerate() {
                let _ = writeln!(
                    output,
                    "rfq_engine_http_request_duration_seconds_bucket{{{labels},le=\"{bound}\"}} {}",
                    sample.buckets[index]
                );
            }
            let _ = writeln!(
                output,
                "rfq_engine_http_request_duration_seconds_bucket{{{labels},le=\"+Inf\"}} {}\nrfq_engine_http_request_duration_seconds_sum{{{labels}}} {}\nrfq_engine_http_request_duration_seconds_count{{{labels}}} {}",
                sample.count, sample.seconds, sample.count
            );
        }
    }
    (
        [(
            header::CONTENT_TYPE,
            "text/plain; version=0.0.4; charset=utf-8",
        )],
        output,
    )
}
