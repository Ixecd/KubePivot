FROM dunglas/frankenphp:1.4-php8.4 AS builder
WORKDIR /app
COPY composer.json .
RUN composer install --no-dev --no-interaction
COPY . .

FROM dunglas/frankenphp:1.4-php8.4
WORKDIR /app
COPY --from=builder /app /app
EXPOSE 8080
CMD ["frankenphp", "run", "--config", "Caddyfile"]
