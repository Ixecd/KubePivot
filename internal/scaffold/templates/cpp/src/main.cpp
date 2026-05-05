#include <httplib.h>

int main() {
    httplib::Server svr;

    svr.Get("/healthz", [](const httplib::Request&, httplib::Response& res) {
        res.set_content("{\"status\":\"ok\"}", "application/json");
    });

    svr.Get("/", [](const httplib::Request&, httplib::Response& res) {
        res.set_content("{\"service\":\"{{name}}\",\"status\":\"ok\"}", "application/json");
    });

    svr.listen("0.0.0.0", 8080);
}
