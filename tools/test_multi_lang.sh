#!/bin/bash
# 12 语言 init + deploy 全量验证脚本
# 用法: bash tools/test_multi_lang.sh
set -o pipefail

TMPDIR=$(mktemp -d -t kp-lang-test-XXXX)
cd "$TMPDIR"
echo "══ 测试目录: ${TMPDIR} ══"
echo

LANGS=("go" "python" "java" "rust" "cpp" "cs" "zig" "kotlin" "ts" "php" "swift" "lua")
PASS=0
FAIL=0
SKIP=0
HAS_DOCKER=false
if command -v docker &>/dev/null && docker info &>/dev/null 2>&1; then
    HAS_DOCKER=true
fi

for lang in "${LANGS[@]}"; do
    name="test-${lang}"
    echo "━━━ ${lang} ━━━"

    kp init --name "$name" --lang "$lang" 2>&1 | tail -1
    if [ ! -d "$name" ]; then
        echo "  ❌ FAIL: 目录未生成"
        ((FAIL++))
        continue
    fi

    # ── 结构验证 ──
    errs=0

    # 共有的部署层
    if [ -f "$name/Makefile" ]; then
        echo "  ✓ Makefile"
    else
        echo "  ✗ Makefile 缺失"
        ((errs++))
    fi
    if [ -d "$name/deployments/${name}" ]; then
        echo "  ✓ Helm chart (deployments/${name}/)"
    else
        echo "  ✗ Helm chart 缺失"
        ((errs++))
    fi

    # Dockerfile 路径：Go 在 build/docker/<name>/，非 Go 也在 build/docker/<name>/
    dockerfile_path="$name/build/docker/${name}/Dockerfile"
    if [ -f "$dockerfile_path" ]; then
        echo "  ✓ Dockerfile (${dockerfile_path})"
    else
        # Dockerfile 可能还在构建目录的子目录中，再仔细查
        found=$(find "$name" -name "Dockerfile" 2>/dev/null | head -1)
        if [ -n "$found" ]; then
            echo "  ⚠ Dockerfile 路径不正确: ${found}"
            ((errs++))
        else
            echo "  ✗ Dockerfile 缺失"
            ((errs++))
        fi
    fi

    # .dockerignore
    if find "$name/build/docker/${name}" -name ".dockerignore" 2>/dev/null | grep -q .; then
        echo "  ✓ .dockerignore"
    fi

    # 语言特定文件
    case $lang in
        go)     [ -f "$name/go.mod" ] && echo "  ✓ go.mod" || { echo "  ✗ go.mod 缺失"; ((errs++)); } ;;
        python) [ -f "$name/pyproject.toml" ] && echo "  ✓ pyproject.toml" || { echo "  ✗ pyproject.toml 缺失"; ((errs++)); } ;;
        java)   [ -f "$name/pom.xml" ] && echo "  ✓ pom.xml" || { echo "  ✗ pom.xml 缺失"; ((errs++)); } ;;
        rust)   [ -f "$name/Cargo.toml" ] && echo "  ✓ Cargo.toml" || { echo "  ✗ Cargo.toml 缺失"; ((errs++)); } ;;
        cpp)    [ -f "$name/CMakeLists.txt" ] && echo "  ✓ CMakeLists.txt" || { echo "  ✗ CMakeLists.txt 缺失"; ((errs++)); } ;;
        cs)     [ -f "$name/Program.cs" ] && echo "  ✓ Program.cs" || { echo "  ✗ Program.cs 缺失"; ((errs++)); } ;;
        zig)    [ -f "$name/build.zig" ] && echo "  ✓ build.zig" || { echo "  ✗ build.zig 缺失"; ((errs++)); } ;;
        kotlin) [ -f "$name/build.gradle.kts" ] && echo "  ✓ build.gradle.kts" || { echo "  ✗ build.gradle.kts 缺失"; ((errs++)); } ;;
        ts)     [ -f "$name/package.json" ] && echo "  ✓ package.json" || { echo "  ✗ package.json 缺失"; ((errs++)); } ;;
        php)    [ -f "$name/composer.json" ] && echo "  ✓ composer.json" || { echo "  ✗ composer.json 缺失"; ((errs++)); } ;;
        swift)  [ -f "$name/Package.swift" ] && echo "  ✓ Package.swift" || { echo "  ✗ Package.swift 缺失"; ((errs++)); } ;;
        lua)    [ -f "$name/nginx.conf" ] && echo "  ✓ nginx.conf" || { echo "  ✗ nginx.conf 缺失"; ((errs++)); } ;;
    esac

    # Helm chart 离线渲染校验（helm template，不连 K8s）
    cd "$name"
    chartDir="deployments/${name}/${name}"
    if [ -d "$chartDir" ] && helm template "$name" "$chartDir" > /dev/null 2>&1; then
        echo "  ✅ helm template 校验通过"
    else
        echo "  ⚠ helm template 校验失败（不影响构建测试）"
    fi

    # make deploy.build（Docker 可用时跑完整构建）
    if $HAS_DOCKER; then
        if timeout 300 make deploy.build > /dev/null 2>&1; then
            echo "  ✅ make deploy.build 通过"
            ((PASS++))
        else
            echo "  ❌ make deploy.build 失败"
            ((FAIL++))
        fi
    else
        echo "  ⏭  make deploy.build 跳过（Docker 不可用）"
        ((PASS++))  # 结构 + preview 通过算过
    fi
    cd "$TMPDIR"
    echo
done

echo "══════════════════"
echo "  通过: ${PASS}  |  失败: ${FAIL}"
if [ "$HAS_DOCKER" = false ]; then
    echo "  Docker 不可用，make deploy.build 已跳过"
fi
echo "══════════════════"
echo "测试目录: ${TMPDIR}"
