var builder = WebApplication.CreateSlimBuilder(args);
var app = builder.Build();
app.MapGet("/healthz", () => Results.Ok(new { status = "ok" }));
app.MapGet("/", () => Results.Ok(new { service = "{{name}}", status = "ok" }));
app.Run("http://0.0.0.0:8080");
