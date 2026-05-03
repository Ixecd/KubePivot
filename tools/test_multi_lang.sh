#!/bin/bash
# 12 语言 init 全量验证脚本
# 用法: bash tools/test_multi_lang.sh
set -o pipefail

TMPDIR=$(mktemp -d -t kp-lang-test-XXXX)
cd "$TMPDIR"
echo "══ 测试目录: $TMPDIR ══"
echo

LANGS=("go" "python" "java" "rust" "cpp" "cs" "zig" "kotlin" "ts" "php" "swift" "lua")
PASS=0
FAIL=0

for lang in "${LANGS[@]}"; do
    name="test-$lang"
    echo "━━━ $lang ━━━"

    kp init --name "$name" --lang "$lang" 2>&1 | tail -1
    if [ ! -d "$name" ]; then
        echo "  ❌ FAIL: 目录未生成"
        ((FAIL++))
        continue
    fi

    # 验证 Dockerfile 存在
    if [ -f "$name/Dockerfile" ]; then
        echo "  ✓ Dockerfile"
    else
        echo "  ⚠ Dockerfile 缺失"
    fi

    # 语言特定文件验证
    case $lang in
        go)     [ -f "$name/go.mod" ] && echo "  ✓ go.mod" || echo "  ✗ go.mod 缺失" ;;
        python) [ -f "$name/pyproject.toml" ] && echo "  ✓ pyproject.toml" || echo "  ✗ pyproject.toml 缺失" ;;
        java)   [ -f "$name/pom.xml" ] && echo "  ✓ pom.xml" || echo "  ✗ pom.xml 缺失" ;;
        rust)   [ -f "$name/Cargo.toml" ] && echo "  ✓ Cargo.toml" || echo "  ✗ Cargo.toml 缺失" ;;
        cpp)    [ -f "$name/CMakeLists.txt" ] && echo "  ✓ CMakeLists.txt" || echo "  ✗ CMakeLists.txt 缺失" ;;
        cs)     [ -f "$name/Program.cs" ] && echo "  ✓ Program.cs" || echo "  ✗ Program.cs 缺失" ;;
        zig)    [ -f "$name/build.zig" ] && echo "  ✓ build.zig" || echo "  ✗ build.zig 缺失" ;;
        kotlin) [ -f "$name/build.gradle.kts" ] && echo "  ✓ build.gradle.kts" || echo "  ✗ build.gradle.kts 缺失" ;;
        ts)     [ -f "$name/package.json" ] && echo "  ✓ package.json" || echo "  ✗ package.json 缺失" ;;
        php)    [ -f "$name/composer.json" ] && echo "  ✓ composer.json" || echo "  ✗ composer.json 缺失" ;;
        swift)  [ -f "$name/Package.swift" ] && echo "  ✓ Package.swift" || echo "  ✗ Package.swift 缺失" ;;
        lua)    [ -f "$name/nginx.conf" ] && echo "  ✓ nginx.conf" || echo "  ✗ nginx.conf 缺失" ;;
    esac

    # kp deploy --dry-run
    cd "$name"
    if kp deploy --dry-run > /dev/null 2>&1; then
        echo "  ✅ deploy --dry-run 通过"
        ((PASS++))
    else
        echo "  ❌ deploy --dry-run 失败"
        ((FAIL++))
    fi
    cd "$TMPDIR"
    echo
done

echo "══════════════════"
echo "  通过: $PASS  |  失败: $FAIL"
echo "══════════════════"
echo "测试目录: $TMPDIR"
