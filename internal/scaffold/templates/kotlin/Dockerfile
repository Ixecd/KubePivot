FROM eclipse-temurin:21-jdk-alpine AS builder
WORKDIR /app
COPY build.gradle.kts gradlew ./
COPY gradle/ ./gradle/
RUN chmod +x gradlew && ./gradlew dependencies --no-daemon
COPY src/ ./src/
RUN ./gradlew buildFatJar --no-daemon

FROM eclipse-temurin:21-jre-alpine
WORKDIR /app
COPY --from=builder /app/build/libs/*.jar app.jar
EXPOSE 8080
CMD ["java", "-Xms256m", "-Xmx512m", "-jar", "app.jar"]
