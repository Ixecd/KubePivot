FROM eclipse-temurin:21-jdk-alpine AS builder
RUN apk add --no-cache gradle
WORKDIR /app
COPY build.gradle.kts .
RUN gradle dependencies --no-daemon
COPY src/ ./src/
RUN gradle buildFatJar --no-daemon

FROM eclipse-temurin:21-jre-alpine
WORKDIR /app
COPY --from=builder /app/build/libs/*.jar app.jar
EXPOSE 8080
CMD ["java", "-Xms256m", "-Xmx512m", "-jar", "app.jar"]
