FROM node:22-slim AS builder
WORKDIR /app
COPY package.json package-lock.json* ./
RUN npm ci --omit=dev
COPY . .

FROM node:22-slim
WORKDIR /app
COPY --from=builder /app /app
USER node
EXPOSE 8080
CMD ["node", "src/index.js"]
