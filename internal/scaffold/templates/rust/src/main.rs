use axum::{Router, routing::get, Json, response::IntoResponse};
use serde::Serialize;

#[derive(Serialize)] struct Status { status: String }
#[derive(Serialize)] struct Home { service: String, status: String }

async fn healthz() -> impl IntoResponse { Json(Status { status: "ok".into() }) }
async fn home() -> impl IntoResponse { Json(Home { service: "{{name}}".into(), status: "ok".into() }) }

#[tokio::main]
async fn main() {
    let app = Router::new().route("/healthz", get(healthz)).route("/", get(home));
    let listener = tokio::net::TcpListener::bind("0.0.0.0:8080").await.unwrap();
    axum::serve(listener, app).await.unwrap();
}
