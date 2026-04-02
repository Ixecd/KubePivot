#!/bin/bash
# gencerts.sh — 生成自签 TLS 证书
# 用法：gencerts.sh generate-cert <output-dir> <name>

set -e

COMMAND=$1
OUTPUT_DIR=$2
NAME=$3

generate_cert() {
    mkdir -p "$OUTPUT_DIR"
    
    echo "===========> 生成 CA 根证书"
    openssl genrsa -out "$OUTPUT_DIR/ca.key" 4096
    openssl req -new -x509 -days 3650 -key "$OUTPUT_DIR/ca.key" \
        -out "$OUTPUT_DIR/ca.crt" \
        -subj "/CN=kubepivot-ca/O=KubePivot"

    echo "===========> 生成 $NAME 证书"
    openssl genrsa -out "$OUTPUT_DIR/$NAME.key" 2048
    openssl req -new -key "$OUTPUT_DIR/$NAME.key" \
        -out "$OUTPUT_DIR/$NAME.csr" \
        -subj "/CN=$NAME/O=KubePivot"

    openssl x509 -req -days 365 \
        -in "$OUTPUT_DIR/$NAME.csr" \
        -CA "$OUTPUT_DIR/ca.crt" \
        -CAkey "$OUTPUT_DIR/ca.key" \
        -CAcreateserial \
        -out "$OUTPUT_DIR/$NAME.crt"

    echo "===========> 证书生成完成：$OUTPUT_DIR/$NAME.crt"
    echo "===========> 有效期：365 天，到期前请运行 kp secret audit"
}

case "$COMMAND" in
    generate-cert)
        generate_cert
        ;;
    *)
        echo "用法: gencerts.sh generate-cert <output-dir> <name>"
        exit 1
        ;;
esac
