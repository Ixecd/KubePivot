const std = @import("std");
pub fn main() !void {
    var gpa = std.heap.GeneralPurposeAllocator(.{}){};
    defer _ = gpa.deinit();
    const addr = try std.net.Address.parseIp4("0.0.0.0", 8080);
    var server = try addr.listen(.{ .reuse_address = true });
    defer server.deinit();
    while (true) {
        const conn = try server.accept();
        defer conn.stream.close();
        var buf: [4096]u8 = undefined;
        const n = try conn.stream.read(&buf);
        const req = buf[0..n];
        if (std.mem.indexOf(u8, req, "GET /healthz") != null) {
            _ = try conn.stream.write("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n{\"status\":\"ok\"}");
        } else {
            _ = try conn.stream.write("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n{\"service\":\"{{name}}\",\"status\":\"ok\"}");
        }
    }
}
