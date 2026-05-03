#include <drogon/drogon.h>
using namespace drogon;

int main() {
    app().registerHandler("/healthz", [](const HttpRequestPtr&,
        std::function<void(const HttpResponsePtr&)>&& callback) {
        Json::Value ret; ret["status"] = "ok";
        callback(HttpResponse::newHttpJsonResponse(ret));
    });
    app().registerHandler("/", [](const HttpRequestPtr&,
        std::function<void(const HttpResponsePtr&)>&& callback) {
        Json::Value ret; ret["service"] = "{{name}}"; ret["status"] = "ok";
        callback(HttpResponse::newHttpJsonResponse(ret));
    });
    app().addListener("0.0.0.0", 8080).run();
    return 0;
}
