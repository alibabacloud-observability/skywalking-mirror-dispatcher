package com.example.demo;

import org.springframework.boot.SpringApplication;
import org.springframework.boot.autoconfigure.SpringBootApplication;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RestController;
import org.springframework.web.client.RestTemplate;

@SpringBootApplication
@RestController
public class DemoApplication {

    private final RestTemplate rt = new RestTemplate();

    public static void main(String[] args) {
        SpringApplication.run(DemoApplication.class, args);
    }

    // /hello calls /work over HTTP so a single request produces a multi-span trace:
    // Tomcat server span (/hello) -> RestTemplate client span -> Tomcat server span (/work).
    @GetMapping("/hello")
    public String hello() {
        String downstream = rt.getForObject("http://localhost:8080/work", String.class);
        return "hello -> " + downstream;
    }

    @GetMapping("/work")
    public String work() throws InterruptedException {
        Thread.sleep(20);
        return "work-done";
    }
}
