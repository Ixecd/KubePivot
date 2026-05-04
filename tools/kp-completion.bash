# KubePivot bash completion — 加到 ~/.bashrc 或 ~/.zshrc
#
#   source tools/kp-completion.bash
#
# Tab 补全规则：
#   kp explain --pod <TAB>  → 列出集群中所有 managed Pod

_kp_completion() {
    local cur prev
    cur="${COMP_WORDS[COMP_CWORD]}"
    prev="${COMP_WORDS[COMP_CWORD-1]}"

    # kp explain --pod 补全
    if [[ "$prev" == "--pod" ]]; then
        local pods
        pods=$(kp explain --list-pods 2>/dev/null)
        COMPREPLY=($(compgen -W "$pods" -- "$cur"))
        return
    fi

    # 顶层子命令补全
    if [[ "$COMP_CWORD" -eq 1 ]]; then
        local cmds="init sync deploy down ai-plan doctor upgrade history diff resume status rollback release scan warmup promote migrate compat pvc network secret policy audit supply-chain sizing chaos plugin version update context sandbox controller explain login team whoami"
        COMPREPLY=($(compgen -W "$cmds" -- "$cur"))
        return
    fi

    # 默认：文件/目录补全
    COMPREPLY=($(compgen -f -- "$cur"))
}

complete -F _kp_completion kp
