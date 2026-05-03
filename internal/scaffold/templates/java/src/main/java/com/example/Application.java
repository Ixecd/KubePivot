package com.example;

import org.springframework.boot.SpringApplication;
import org.springframework.boot.autoconfigure.SpringBootApplication;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RestController;

@SpringBootApplication
@RestController
public class Application {
    public static void main(String[] args) {
        SpringApplication.run(Application.class, args);
    }

    @GetMapping("/healthz")
    public String healthz() { return "ok"; }

    @GetMapping("/")
    public java.util.Map<String, String> home() {
        return java.util.Map.of("service", "{{name}}", "status", "ok");
    }
}
