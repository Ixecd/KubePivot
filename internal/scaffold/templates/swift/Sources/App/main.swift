import Vapor
let app = try Application(.detect())
defer { app.shutdown() }
app.get("healthz") { _ in ["status": "ok"] }
app.get { _ in ["service": "{{name}}", "status": "ok"] }
try app.run()
