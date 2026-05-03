# ── GPU 训练任务 ──────────────────────────────────────────────
# 如果你要用 GPU，请：
# 1. 替换基础镜像：
#    FROM nvidia/cuda:12.4.1-runtime-ubuntu22.04
# 2. 安装对应 CUDA 版本的 PyTorch：
#    RUN pip install torch --index-url https://download.pytorch.org/whl/cu124
# 3. 在 resources.yaml 中配置 gpu 字段
# ──────────────────────────────────────────────────────────────

FROM python:3.12-slim AS builder
WORKDIR /app
COPY pyproject.toml .
RUN pip install --no-cache-dir --prefix=/install .

FROM python:3.12-slim
COPY --from=builder /install /usr/local
WORKDIR /app
COPY app/ ./app/
EXPOSE 8080
# 开发环境：单进程 uvicorn
CMD ["uvicorn", "app.main:app", "--host", "0.0.0.0", "--port", "8080"]
# 生产环境：多 worker gunicorn（取消注释）
# CMD ["gunicorn", "app.main:app", "-w", "4", "-k", "uvicorn.workers.UvicornWorker", "--bind", "0.0.0.0:8080"]
