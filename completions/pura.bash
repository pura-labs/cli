# bash completion for pura                                 -*- shell-script -*-

__pura_debug()
{
    if [[ -n ${BASH_COMP_DEBUG_FILE:-} ]]; then
        echo "$*" >> "${BASH_COMP_DEBUG_FILE}"
    fi
}

# Homebrew on Macs have version 1.3 of bash-completion which doesn't include
# _init_completion. This is a very minimal version of that function.
__pura_init_completion()
{
    COMPREPLY=()
    _get_comp_words_by_ref "$@" cur prev words cword
}

__pura_index_of_word()
{
    local w word=$1
    shift
    index=0
    for w in "$@"; do
        [[ $w = "$word" ]] && return
        index=$((index+1))
    done
    index=-1
}

__pura_contains_word()
{
    local w word=$1; shift
    for w in "$@"; do
        [[ $w = "$word" ]] && return
    done
    return 1
}

__pura_handle_go_custom_completion()
{
    __pura_debug "${FUNCNAME[0]}: cur is ${cur}, words[*] is ${words[*]}, #words[@] is ${#words[@]}"

    local shellCompDirectiveError=1
    local shellCompDirectiveNoSpace=2
    local shellCompDirectiveNoFileComp=4
    local shellCompDirectiveFilterFileExt=8
    local shellCompDirectiveFilterDirs=16

    local out requestComp lastParam lastChar comp directive args

    # Prepare the command to request completions for the program.
    # Calling ${words[0]} instead of directly pura allows handling aliases
    args=("${words[@]:1}")
    # Disable ActiveHelp which is not supported for bash completion v1
    requestComp="PURA_ACTIVE_HELP=0 ${words[0]} __completeNoDesc ${args[*]}"

    lastParam=${words[$((${#words[@]}-1))]}
    lastChar=${lastParam:$((${#lastParam}-1)):1}
    __pura_debug "${FUNCNAME[0]}: lastParam ${lastParam}, lastChar ${lastChar}"

    if [ -z "${cur}" ] && [ "${lastChar}" != "=" ]; then
        # If the last parameter is complete (there is a space following it)
        # We add an extra empty parameter so we can indicate this to the go method.
        __pura_debug "${FUNCNAME[0]}: Adding extra empty parameter"
        requestComp="${requestComp} \"\""
    fi

    __pura_debug "${FUNCNAME[0]}: calling ${requestComp}"
    # Use eval to handle any environment variables and such
    out=$(eval "${requestComp}" 2>/dev/null)

    # Extract the directive integer at the very end of the output following a colon (:)
    directive=${out##*:}
    # Remove the directive
    out=${out%:*}
    if [ "${directive}" = "${out}" ]; then
        # There is not directive specified
        directive=0
    fi
    __pura_debug "${FUNCNAME[0]}: the completion directive is: ${directive}"
    __pura_debug "${FUNCNAME[0]}: the completions are: ${out}"

    if [ $((directive & shellCompDirectiveError)) -ne 0 ]; then
        # Error code.  No completion.
        __pura_debug "${FUNCNAME[0]}: received error from custom completion go code"
        return
    else
        if [ $((directive & shellCompDirectiveNoSpace)) -ne 0 ]; then
            if [[ $(type -t compopt) = "builtin" ]]; then
                __pura_debug "${FUNCNAME[0]}: activating no space"
                compopt -o nospace
            fi
        fi
        if [ $((directive & shellCompDirectiveNoFileComp)) -ne 0 ]; then
            if [[ $(type -t compopt) = "builtin" ]]; then
                __pura_debug "${FUNCNAME[0]}: activating no file completion"
                compopt +o default
            fi
        fi
    fi

    if [ $((directive & shellCompDirectiveFilterFileExt)) -ne 0 ]; then
        # File extension filtering
        local fullFilter filter filteringCmd
        # Do not use quotes around the $out variable or else newline
        # characters will be kept.
        for filter in ${out}; do
            fullFilter+="$filter|"
        done

        filteringCmd="_filedir $fullFilter"
        __pura_debug "File filtering command: $filteringCmd"
        $filteringCmd
    elif [ $((directive & shellCompDirectiveFilterDirs)) -ne 0 ]; then
        # File completion for directories only
        local subdir
        # Use printf to strip any trailing newline
        subdir=$(printf "%s" "${out}")
        if [ -n "$subdir" ]; then
            __pura_debug "Listing directories in $subdir"
            __pura_handle_subdirs_in_dir_flag "$subdir"
        else
            __pura_debug "Listing directories in ."
            _filedir -d
        fi
    else
        while IFS='' read -r comp; do
            COMPREPLY+=("$comp")
        done < <(compgen -W "${out}" -- "$cur")
    fi
}

__pura_handle_reply()
{
    __pura_debug "${FUNCNAME[0]}"
    local comp
    case $cur in
        -*)
            if [[ $(type -t compopt) = "builtin" ]]; then
                compopt -o nospace
            fi
            local allflags
            if [ ${#must_have_one_flag[@]} -ne 0 ]; then
                allflags=("${must_have_one_flag[@]}")
            else
                allflags=("${flags[*]} ${two_word_flags[*]}")
            fi
            while IFS='' read -r comp; do
                COMPREPLY+=("$comp")
            done < <(compgen -W "${allflags[*]}" -- "$cur")
            if [[ $(type -t compopt) = "builtin" ]]; then
                [[ "${COMPREPLY[0]}" == *= ]] || compopt +o nospace
            fi

            # complete after --flag=abc
            if [[ $cur == *=* ]]; then
                if [[ $(type -t compopt) = "builtin" ]]; then
                    compopt +o nospace
                fi

                local index flag
                flag="${cur%=*}"
                __pura_index_of_word "${flag}" "${flags_with_completion[@]}"
                COMPREPLY=()
                if [[ ${index} -ge 0 ]]; then
                    PREFIX=""
                    cur="${cur#*=}"
                    ${flags_completion[${index}]}
                    if [ -n "${ZSH_VERSION:-}" ]; then
                        # zsh completion needs --flag= prefix
                        eval "COMPREPLY=( \"\${COMPREPLY[@]/#/${flag}=}\" )"
                    fi
                fi
            fi

            if [[ -z "${flag_parsing_disabled}" ]]; then
                # If flag parsing is enabled, we have completed the flags and can return.
                # If flag parsing is disabled, we may not know all (or any) of the flags, so we fallthrough
                # to possibly call handle_go_custom_completion.
                return 0;
            fi
            ;;
    esac

    # check if we are handling a flag with special work handling
    local index
    __pura_index_of_word "${prev}" "${flags_with_completion[@]}"
    if [[ ${index} -ge 0 ]]; then
        ${flags_completion[${index}]}
        return
    fi

    # we are parsing a flag and don't have a special handler, no completion
    if [[ ${cur} != "${words[cword]}" ]]; then
        return
    fi

    local completions
    completions=("${commands[@]}")
    if [[ ${#must_have_one_noun[@]} -ne 0 ]]; then
        completions+=("${must_have_one_noun[@]}")
    elif [[ -n "${has_completion_function}" ]]; then
        # if a go completion function is provided, defer to that function
        __pura_handle_go_custom_completion
    fi
    if [[ ${#must_have_one_flag[@]} -ne 0 ]]; then
        completions+=("${must_have_one_flag[@]}")
    fi
    while IFS='' read -r comp; do
        COMPREPLY+=("$comp")
    done < <(compgen -W "${completions[*]}" -- "$cur")

    if [[ ${#COMPREPLY[@]} -eq 0 && ${#noun_aliases[@]} -gt 0 && ${#must_have_one_noun[@]} -ne 0 ]]; then
        while IFS='' read -r comp; do
            COMPREPLY+=("$comp")
        done < <(compgen -W "${noun_aliases[*]}" -- "$cur")
    fi

    if [[ ${#COMPREPLY[@]} -eq 0 ]]; then
        if declare -F __pura_custom_func >/dev/null; then
            # try command name qualified custom func
            __pura_custom_func
        else
            # otherwise fall back to unqualified for compatibility
            declare -F __custom_func >/dev/null && __custom_func
        fi
    fi

    # available in bash-completion >= 2, not always present on macOS
    if declare -F __ltrim_colon_completions >/dev/null; then
        __ltrim_colon_completions "$cur"
    fi

    # If there is only 1 completion and it is a flag with an = it will be completed
    # but we don't want a space after the =
    if [[ "${#COMPREPLY[@]}" -eq "1" ]] && [[ $(type -t compopt) = "builtin" ]] && [[ "${COMPREPLY[0]}" == --*= ]]; then
       compopt -o nospace
    fi
}

# The arguments should be in the form "ext1|ext2|extn"
__pura_handle_filename_extension_flag()
{
    local ext="$1"
    _filedir "@(${ext})"
}

__pura_handle_subdirs_in_dir_flag()
{
    local dir="$1"
    pushd "${dir}" >/dev/null 2>&1 && _filedir -d && popd >/dev/null 2>&1 || return
}

__pura_handle_flag()
{
    __pura_debug "${FUNCNAME[0]}: c is $c words[c] is ${words[c]}"

    # if a command required a flag, and we found it, unset must_have_one_flag()
    local flagname=${words[c]}
    local flagvalue=""
    # if the word contained an =
    if [[ ${words[c]} == *"="* ]]; then
        flagvalue=${flagname#*=} # take in as flagvalue after the =
        flagname=${flagname%=*} # strip everything after the =
        flagname="${flagname}=" # but put the = back
    fi
    __pura_debug "${FUNCNAME[0]}: looking for ${flagname}"
    if __pura_contains_word "${flagname}" "${must_have_one_flag[@]}"; then
        must_have_one_flag=()
    fi

    # if you set a flag which only applies to this command, don't show subcommands
    if __pura_contains_word "${flagname}" "${local_nonpersistent_flags[@]}"; then
      commands=()
    fi

    # keep flag value with flagname as flaghash
    # flaghash variable is an associative array which is only supported in bash > 3.
    if [[ -z "${BASH_VERSION:-}" || "${BASH_VERSINFO[0]:-}" -gt 3 ]]; then
        if [ -n "${flagvalue}" ] ; then
            flaghash[${flagname}]=${flagvalue}
        elif [ -n "${words[ $((c+1)) ]}" ] ; then
            flaghash[${flagname}]=${words[ $((c+1)) ]}
        else
            flaghash[${flagname}]="true" # pad "true" for bool flag
        fi
    fi

    # skip the argument to a two word flag
    if [[ ${words[c]} != *"="* ]] && __pura_contains_word "${words[c]}" "${two_word_flags[@]}"; then
        __pura_debug "${FUNCNAME[0]}: found a flag ${words[c]}, skip the next argument"
        c=$((c+1))
        # if we are looking for a flags value, don't show commands
        if [[ $c -eq $cword ]]; then
            commands=()
        fi
    fi

    c=$((c+1))

}

__pura_handle_noun()
{
    __pura_debug "${FUNCNAME[0]}: c is $c words[c] is ${words[c]}"

    if __pura_contains_word "${words[c]}" "${must_have_one_noun[@]}"; then
        must_have_one_noun=()
    elif __pura_contains_word "${words[c]}" "${noun_aliases[@]}"; then
        must_have_one_noun=()
    fi

    nouns+=("${words[c]}")
    c=$((c+1))
}

__pura_handle_command()
{
    __pura_debug "${FUNCNAME[0]}: c is $c words[c] is ${words[c]}"

    local next_command
    if [[ -n ${last_command} ]]; then
        next_command="_${last_command}_${words[c]//:/__}"
    else
        if [[ $c -eq 0 ]]; then
            next_command="_pura_root_command"
        else
            next_command="_${words[c]//:/__}"
        fi
    fi
    c=$((c+1))
    __pura_debug "${FUNCNAME[0]}: looking for ${next_command}"
    declare -F "$next_command" >/dev/null && $next_command
}

__pura_handle_word()
{
    if [[ $c -ge $cword ]]; then
        __pura_handle_reply
        return
    fi
    __pura_debug "${FUNCNAME[0]}: c is $c words[c] is ${words[c]}"
    if [[ "${words[c]}" == -* ]]; then
        __pura_handle_flag
    elif __pura_contains_word "${words[c]}" "${commands[@]}"; then
        __pura_handle_command
    elif [[ $c -eq 0 ]]; then
        __pura_handle_command
    elif __pura_contains_word "${words[c]}" "${command_aliases[@]}"; then
        # aliashash variable is an associative array which is only supported in bash > 3.
        if [[ -z "${BASH_VERSION:-}" || "${BASH_VERSINFO[0]:-}" -gt 3 ]]; then
            words[c]=${aliashash[${words[c]}]}
            __pura_handle_command
        else
            __pura_handle_noun
        fi
    else
        __pura_handle_noun
    fi
    __pura_handle_word
}

_pura_auth_login()
{
    last_command="pura_auth_login"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--no-browser")
    local_nonpersistent_flags+=("--no-browser")
    flags+=("--scope=")
    two_word_flags+=("--scope")
    local_nonpersistent_flags+=("--scope")
    local_nonpersistent_flags+=("--scope=")
    flags+=("--timeout=")
    two_word_flags+=("--timeout")
    local_nonpersistent_flags+=("--timeout")
    local_nonpersistent_flags+=("--timeout=")
    flags+=("--token=")
    two_word_flags+=("--token")
    local_nonpersistent_flags+=("--token")
    local_nonpersistent_flags+=("--token=")
    flags+=("--api-url=")
    two_word_flags+=("--api-url")
    flags+=("--handle=")
    two_word_flags+=("--handle")
    flags+=("--jq=")
    two_word_flags+=("--jq")
    flags+=("--json")
    flags+=("--profile=")
    two_word_flags+=("--profile")
    flags+=("--quiet")
    flags+=("--verbose")
    flags+=("-v")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_pura_auth_logout()
{
    last_command="pura_auth_logout"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--api-url=")
    two_word_flags+=("--api-url")
    flags+=("--handle=")
    two_word_flags+=("--handle")
    flags+=("--jq=")
    two_word_flags+=("--jq")
    flags+=("--json")
    flags+=("--profile=")
    two_word_flags+=("--profile")
    flags+=("--quiet")
    flags+=("--token=")
    two_word_flags+=("--token")
    flags+=("--verbose")
    flags+=("-v")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_pura_auth_refresh()
{
    last_command="pura_auth_refresh"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--yes")
    local_nonpersistent_flags+=("--yes")
    flags+=("--api-url=")
    two_word_flags+=("--api-url")
    flags+=("--handle=")
    two_word_flags+=("--handle")
    flags+=("--jq=")
    two_word_flags+=("--jq")
    flags+=("--json")
    flags+=("--profile=")
    two_word_flags+=("--profile")
    flags+=("--quiet")
    flags+=("--token=")
    two_word_flags+=("--token")
    flags+=("--verbose")
    flags+=("-v")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_pura_auth_status()
{
    last_command="pura_auth_status"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--verify")
    local_nonpersistent_flags+=("--verify")
    flags+=("--api-url=")
    two_word_flags+=("--api-url")
    flags+=("--handle=")
    two_word_flags+=("--handle")
    flags+=("--jq=")
    two_word_flags+=("--jq")
    flags+=("--json")
    flags+=("--profile=")
    two_word_flags+=("--profile")
    flags+=("--quiet")
    flags+=("--token=")
    two_word_flags+=("--token")
    flags+=("--verbose")
    flags+=("-v")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_pura_auth_token()
{
    last_command="pura_auth_token"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--yes")
    local_nonpersistent_flags+=("--yes")
    flags+=("--api-url=")
    two_word_flags+=("--api-url")
    flags+=("--handle=")
    two_word_flags+=("--handle")
    flags+=("--jq=")
    two_word_flags+=("--jq")
    flags+=("--json")
    flags+=("--profile=")
    two_word_flags+=("--profile")
    flags+=("--quiet")
    flags+=("--token=")
    two_word_flags+=("--token")
    flags+=("--verbose")
    flags+=("-v")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_pura_auth()
{
    last_command="pura_auth"

    command_aliases=()

    commands=()
    commands+=("login")
    commands+=("logout")
    commands+=("refresh")
    commands+=("status")
    commands+=("token")

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--api-url=")
    two_word_flags+=("--api-url")
    flags+=("--handle=")
    two_word_flags+=("--handle")
    flags+=("--jq=")
    two_word_flags+=("--jq")
    flags+=("--json")
    flags+=("--profile=")
    two_word_flags+=("--profile")
    flags+=("--quiet")
    flags+=("--token=")
    two_word_flags+=("--token")
    flags+=("--verbose")
    flags+=("-v")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_pura_book_add()
{
    last_command="pura_book_add"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--anchor=")
    two_word_flags+=("--anchor")
    local_nonpersistent_flags+=("--anchor")
    local_nonpersistent_flags+=("--anchor=")
    flags+=("--position=")
    two_word_flags+=("--position")
    local_nonpersistent_flags+=("--position")
    local_nonpersistent_flags+=("--position=")
    flags+=("--api-url=")
    two_word_flags+=("--api-url")
    flags+=("--handle=")
    two_word_flags+=("--handle")
    flags+=("--jq=")
    two_word_flags+=("--jq")
    flags+=("--json")
    flags+=("--profile=")
    two_word_flags+=("--profile")
    flags+=("--quiet")
    flags+=("--token=")
    two_word_flags+=("--token")
    flags+=("--verbose")
    flags+=("-v")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_pura_book_create()
{
    last_command="pura_book_create"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--author=")
    two_word_flags+=("--author")
    local_nonpersistent_flags+=("--author")
    local_nonpersistent_flags+=("--author=")
    flags+=("--subtitle=")
    two_word_flags+=("--subtitle")
    local_nonpersistent_flags+=("--subtitle")
    local_nonpersistent_flags+=("--subtitle=")
    flags+=("--theme=")
    two_word_flags+=("--theme")
    local_nonpersistent_flags+=("--theme")
    local_nonpersistent_flags+=("--theme=")
    flags+=("--title=")
    two_word_flags+=("--title")
    local_nonpersistent_flags+=("--title")
    local_nonpersistent_flags+=("--title=")
    flags+=("--api-url=")
    two_word_flags+=("--api-url")
    flags+=("--handle=")
    two_word_flags+=("--handle")
    flags+=("--jq=")
    two_word_flags+=("--jq")
    flags+=("--json")
    flags+=("--profile=")
    two_word_flags+=("--profile")
    flags+=("--quiet")
    flags+=("--token=")
    two_word_flags+=("--token")
    flags+=("--verbose")
    flags+=("-v")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_pura_book_export()
{
    last_command="pura_book_export"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--format=")
    two_word_flags+=("--format")
    local_nonpersistent_flags+=("--format")
    local_nonpersistent_flags+=("--format=")
    flags+=("--out=")
    two_word_flags+=("--out")
    local_nonpersistent_flags+=("--out")
    local_nonpersistent_flags+=("--out=")
    flags+=("--api-url=")
    two_word_flags+=("--api-url")
    flags+=("--handle=")
    two_word_flags+=("--handle")
    flags+=("--jq=")
    two_word_flags+=("--jq")
    flags+=("--json")
    flags+=("--profile=")
    two_word_flags+=("--profile")
    flags+=("--quiet")
    flags+=("--token=")
    two_word_flags+=("--token")
    flags+=("--verbose")
    flags+=("-v")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_pura_book_read()
{
    last_command="pura_book_read"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--api-url=")
    two_word_flags+=("--api-url")
    flags+=("--handle=")
    two_word_flags+=("--handle")
    flags+=("--jq=")
    two_word_flags+=("--jq")
    flags+=("--json")
    flags+=("--profile=")
    two_word_flags+=("--profile")
    flags+=("--quiet")
    flags+=("--token=")
    two_word_flags+=("--token")
    flags+=("--verbose")
    flags+=("-v")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_pura_book_reorder()
{
    last_command="pura_book_reorder"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--api-url=")
    two_word_flags+=("--api-url")
    flags+=("--handle=")
    two_word_flags+=("--handle")
    flags+=("--jq=")
    two_word_flags+=("--jq")
    flags+=("--json")
    flags+=("--profile=")
    two_word_flags+=("--profile")
    flags+=("--quiet")
    flags+=("--token=")
    two_word_flags+=("--token")
    flags+=("--verbose")
    flags+=("-v")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_pura_book_rm()
{
    last_command="pura_book_rm"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--api-url=")
    two_word_flags+=("--api-url")
    flags+=("--handle=")
    two_word_flags+=("--handle")
    flags+=("--jq=")
    two_word_flags+=("--jq")
    flags+=("--json")
    flags+=("--profile=")
    two_word_flags+=("--profile")
    flags+=("--quiet")
    flags+=("--token=")
    two_word_flags+=("--token")
    flags+=("--verbose")
    flags+=("-v")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_pura_book()
{
    last_command="pura_book"

    command_aliases=()

    commands=()
    commands+=("add")
    commands+=("create")
    commands+=("export")
    commands+=("read")
    commands+=("reorder")
    commands+=("rm")

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--api-url=")
    two_word_flags+=("--api-url")
    flags+=("--handle=")
    two_word_flags+=("--handle")
    flags+=("--jq=")
    two_word_flags+=("--jq")
    flags+=("--json")
    flags+=("--profile=")
    two_word_flags+=("--profile")
    flags+=("--quiet")
    flags+=("--token=")
    two_word_flags+=("--token")
    flags+=("--verbose")
    flags+=("-v")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_pura_chat()
{
    last_command="pura_chat"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--dry-run")
    local_nonpersistent_flags+=("--dry-run")
    flags+=("--interactive")
    local_nonpersistent_flags+=("--interactive")
    flags+=("--model=")
    two_word_flags+=("--model")
    local_nonpersistent_flags+=("--model")
    local_nonpersistent_flags+=("--model=")
    flags+=("--no-stream")
    local_nonpersistent_flags+=("--no-stream")
    flags+=("--resolve=")
    two_word_flags+=("--resolve")
    local_nonpersistent_flags+=("--resolve")
    local_nonpersistent_flags+=("--resolve=")
    flags+=("--selection=")
    two_word_flags+=("--selection")
    local_nonpersistent_flags+=("--selection")
    local_nonpersistent_flags+=("--selection=")
    flags+=("--yes")
    local_nonpersistent_flags+=("--yes")
    flags+=("--api-url=")
    two_word_flags+=("--api-url")
    flags+=("--handle=")
    two_word_flags+=("--handle")
    flags+=("--jq=")
    two_word_flags+=("--jq")
    flags+=("--json")
    flags+=("--profile=")
    two_word_flags+=("--profile")
    flags+=("--quiet")
    flags+=("--token=")
    two_word_flags+=("--token")
    flags+=("--verbose")
    flags+=("-v")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_pura_claim()
{
    last_command="pura_claim"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--api-url=")
    two_word_flags+=("--api-url")
    flags+=("--handle=")
    two_word_flags+=("--handle")
    flags+=("--jq=")
    two_word_flags+=("--jq")
    flags+=("--json")
    flags+=("--profile=")
    two_word_flags+=("--profile")
    flags+=("--quiet")
    flags+=("--token=")
    two_word_flags+=("--token")
    flags+=("--verbose")
    flags+=("-v")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_pura_completion()
{
    last_command="pura_completion"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--help")
    flags+=("-h")
    local_nonpersistent_flags+=("--help")
    local_nonpersistent_flags+=("-h")
    flags+=("--api-url=")
    two_word_flags+=("--api-url")
    flags+=("--handle=")
    two_word_flags+=("--handle")
    flags+=("--jq=")
    two_word_flags+=("--jq")
    flags+=("--json")
    flags+=("--profile=")
    two_word_flags+=("--profile")
    flags+=("--quiet")
    flags+=("--token=")
    two_word_flags+=("--token")
    flags+=("--verbose")
    flags+=("-v")

    must_have_one_flag=()
    must_have_one_noun=()
    must_have_one_noun+=("bash")
    must_have_one_noun+=("fish")
    must_have_one_noun+=("powershell")
    must_have_one_noun+=("zsh")
    noun_aliases=()
}

_pura_config_get()
{
    last_command="pura_config_get"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--api-url=")
    two_word_flags+=("--api-url")
    flags+=("--handle=")
    two_word_flags+=("--handle")
    flags+=("--jq=")
    two_word_flags+=("--jq")
    flags+=("--json")
    flags+=("--profile=")
    two_word_flags+=("--profile")
    flags+=("--quiet")
    flags+=("--token=")
    two_word_flags+=("--token")
    flags+=("--verbose")
    flags+=("-v")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_pura_config_list()
{
    last_command="pura_config_list"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--api-url=")
    two_word_flags+=("--api-url")
    flags+=("--handle=")
    two_word_flags+=("--handle")
    flags+=("--jq=")
    two_word_flags+=("--jq")
    flags+=("--json")
    flags+=("--profile=")
    two_word_flags+=("--profile")
    flags+=("--quiet")
    flags+=("--token=")
    two_word_flags+=("--token")
    flags+=("--verbose")
    flags+=("-v")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_pura_config_set()
{
    last_command="pura_config_set"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--api-url=")
    two_word_flags+=("--api-url")
    flags+=("--handle=")
    two_word_flags+=("--handle")
    flags+=("--jq=")
    two_word_flags+=("--jq")
    flags+=("--json")
    flags+=("--profile=")
    two_word_flags+=("--profile")
    flags+=("--quiet")
    flags+=("--token=")
    two_word_flags+=("--token")
    flags+=("--verbose")
    flags+=("-v")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_pura_config()
{
    last_command="pura_config"

    command_aliases=()

    commands=()
    commands+=("get")
    commands+=("list")
    if [[ -z "${BASH_VERSION:-}" || "${BASH_VERSINFO[0]:-}" -gt 3 ]]; then
        command_aliases+=("ls")
        aliashash["ls"]="list"
    fi
    commands+=("set")

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--api-url=")
    two_word_flags+=("--api-url")
    flags+=("--handle=")
    two_word_flags+=("--handle")
    flags+=("--jq=")
    two_word_flags+=("--jq")
    flags+=("--json")
    flags+=("--profile=")
    two_word_flags+=("--profile")
    flags+=("--quiet")
    flags+=("--token=")
    two_word_flags+=("--token")
    flags+=("--verbose")
    flags+=("-v")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_pura_doctor()
{
    last_command="pura_doctor"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--api-url=")
    two_word_flags+=("--api-url")
    flags+=("--handle=")
    two_word_flags+=("--handle")
    flags+=("--jq=")
    two_word_flags+=("--jq")
    flags+=("--json")
    flags+=("--profile=")
    two_word_flags+=("--profile")
    flags+=("--quiet")
    flags+=("--token=")
    two_word_flags+=("--token")
    flags+=("--verbose")
    flags+=("-v")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_pura_edit()
{
    last_command="pura_edit"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--file=")
    two_word_flags+=("--file")
    local_nonpersistent_flags+=("--file")
    local_nonpersistent_flags+=("--file=")
    flags+=("--stdin")
    local_nonpersistent_flags+=("--stdin")
    flags+=("--theme=")
    two_word_flags+=("--theme")
    local_nonpersistent_flags+=("--theme")
    local_nonpersistent_flags+=("--theme=")
    flags+=("--title=")
    two_word_flags+=("--title")
    local_nonpersistent_flags+=("--title")
    local_nonpersistent_flags+=("--title=")
    flags+=("--api-url=")
    two_word_flags+=("--api-url")
    flags+=("--handle=")
    two_word_flags+=("--handle")
    flags+=("--jq=")
    two_word_flags+=("--jq")
    flags+=("--json")
    flags+=("--profile=")
    two_word_flags+=("--profile")
    flags+=("--quiet")
    flags+=("--token=")
    two_word_flags+=("--token")
    flags+=("--verbose")
    flags+=("-v")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_pura_events()
{
    last_command="pura_events"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--follow")
    flags+=("-f")
    local_nonpersistent_flags+=("--follow")
    local_nonpersistent_flags+=("-f")
    flags+=("--kinds=")
    two_word_flags+=("--kinds")
    local_nonpersistent_flags+=("--kinds")
    local_nonpersistent_flags+=("--kinds=")
    flags+=("--limit=")
    two_word_flags+=("--limit")
    local_nonpersistent_flags+=("--limit")
    local_nonpersistent_flags+=("--limit=")
    flags+=("--poll-interval=")
    two_word_flags+=("--poll-interval")
    local_nonpersistent_flags+=("--poll-interval")
    local_nonpersistent_flags+=("--poll-interval=")
    flags+=("--since=")
    two_word_flags+=("--since")
    local_nonpersistent_flags+=("--since")
    local_nonpersistent_flags+=("--since=")
    flags+=("--api-url=")
    two_word_flags+=("--api-url")
    flags+=("--handle=")
    two_word_flags+=("--handle")
    flags+=("--jq=")
    two_word_flags+=("--jq")
    flags+=("--json")
    flags+=("--profile=")
    two_word_flags+=("--profile")
    flags+=("--quiet")
    flags+=("--token=")
    two_word_flags+=("--token")
    flags+=("--verbose")
    flags+=("-v")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_pura_get()
{
    last_command="pura_get"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--format=")
    two_word_flags+=("--format")
    two_word_flags+=("-f")
    local_nonpersistent_flags+=("--format")
    local_nonpersistent_flags+=("--format=")
    local_nonpersistent_flags+=("-f")
    flags+=("--output=")
    two_word_flags+=("--output")
    two_word_flags+=("-o")
    local_nonpersistent_flags+=("--output")
    local_nonpersistent_flags+=("--output=")
    local_nonpersistent_flags+=("-o")
    flags+=("--api-url=")
    two_word_flags+=("--api-url")
    flags+=("--handle=")
    two_word_flags+=("--handle")
    flags+=("--jq=")
    two_word_flags+=("--jq")
    flags+=("--json")
    flags+=("--profile=")
    two_word_flags+=("--profile")
    flags+=("--quiet")
    flags+=("--token=")
    two_word_flags+=("--token")
    flags+=("--verbose")
    flags+=("-v")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_pura_help()
{
    last_command="pura_help"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--api-url=")
    two_word_flags+=("--api-url")
    flags+=("--handle=")
    two_word_flags+=("--handle")
    flags+=("--jq=")
    two_word_flags+=("--jq")
    flags+=("--json")
    flags+=("--profile=")
    two_word_flags+=("--profile")
    flags+=("--quiet")
    flags+=("--token=")
    two_word_flags+=("--token")
    flags+=("--verbose")
    flags+=("-v")

    must_have_one_flag=()
    must_have_one_noun=()
    has_completion_function=1
    noun_aliases=()
}

_pura_keys_create()
{
    last_command="pura_keys_create"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--name=")
    two_word_flags+=("--name")
    two_word_flags+=("-n")
    local_nonpersistent_flags+=("--name")
    local_nonpersistent_flags+=("--name=")
    local_nonpersistent_flags+=("-n")
    flags+=("--scope=")
    two_word_flags+=("--scope")
    local_nonpersistent_flags+=("--scope")
    local_nonpersistent_flags+=("--scope=")
    flags+=("--api-url=")
    two_word_flags+=("--api-url")
    flags+=("--handle=")
    two_word_flags+=("--handle")
    flags+=("--jq=")
    two_word_flags+=("--jq")
    flags+=("--json")
    flags+=("--profile=")
    two_word_flags+=("--profile")
    flags+=("--quiet")
    flags+=("--token=")
    two_word_flags+=("--token")
    flags+=("--verbose")
    flags+=("-v")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_pura_keys_ls()
{
    last_command="pura_keys_ls"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--api-url=")
    two_word_flags+=("--api-url")
    flags+=("--handle=")
    two_word_flags+=("--handle")
    flags+=("--jq=")
    two_word_flags+=("--jq")
    flags+=("--json")
    flags+=("--profile=")
    two_word_flags+=("--profile")
    flags+=("--quiet")
    flags+=("--token=")
    two_word_flags+=("--token")
    flags+=("--verbose")
    flags+=("-v")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_pura_keys_rm()
{
    last_command="pura_keys_rm"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--yes")
    local_nonpersistent_flags+=("--yes")
    flags+=("--api-url=")
    two_word_flags+=("--api-url")
    flags+=("--handle=")
    two_word_flags+=("--handle")
    flags+=("--jq=")
    two_word_flags+=("--jq")
    flags+=("--json")
    flags+=("--profile=")
    two_word_flags+=("--profile")
    flags+=("--quiet")
    flags+=("--token=")
    two_word_flags+=("--token")
    flags+=("--verbose")
    flags+=("-v")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_pura_keys()
{
    last_command="pura_keys"

    command_aliases=()

    commands=()
    commands+=("create")
    commands+=("ls")
    if [[ -z "${BASH_VERSION:-}" || "${BASH_VERSINFO[0]:-}" -gt 3 ]]; then
        command_aliases+=("list")
        aliashash["list"]="ls"
    fi
    commands+=("rm")

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--api-url=")
    two_word_flags+=("--api-url")
    flags+=("--handle=")
    two_word_flags+=("--handle")
    flags+=("--jq=")
    two_word_flags+=("--jq")
    flags+=("--json")
    flags+=("--profile=")
    two_word_flags+=("--profile")
    flags+=("--quiet")
    flags+=("--token=")
    two_word_flags+=("--token")
    flags+=("--verbose")
    flags+=("-v")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_pura_ls()
{
    last_command="pura_ls"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--api-url=")
    two_word_flags+=("--api-url")
    flags+=("--handle=")
    two_word_flags+=("--handle")
    flags+=("--jq=")
    two_word_flags+=("--jq")
    flags+=("--json")
    flags+=("--profile=")
    two_word_flags+=("--profile")
    flags+=("--quiet")
    flags+=("--token=")
    two_word_flags+=("--token")
    flags+=("--verbose")
    flags+=("-v")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_pura_mcp_config()
{
    last_command="pura_mcp_config"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--client=")
    two_word_flags+=("--client")
    local_nonpersistent_flags+=("--client")
    local_nonpersistent_flags+=("--client=")
    flags+=("--for-copy")
    local_nonpersistent_flags+=("--for-copy")
    flags+=("--scope=")
    two_word_flags+=("--scope")
    local_nonpersistent_flags+=("--scope")
    local_nonpersistent_flags+=("--scope=")
    flags+=("--transport=")
    two_word_flags+=("--transport")
    local_nonpersistent_flags+=("--transport")
    local_nonpersistent_flags+=("--transport=")
    flags+=("--api-url=")
    two_word_flags+=("--api-url")
    flags+=("--handle=")
    two_word_flags+=("--handle")
    flags+=("--jq=")
    two_word_flags+=("--jq")
    flags+=("--json")
    flags+=("--profile=")
    two_word_flags+=("--profile")
    flags+=("--quiet")
    flags+=("--token=")
    two_word_flags+=("--token")
    flags+=("--verbose")
    flags+=("-v")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_pura_mcp_doctor()
{
    last_command="pura_mcp_doctor"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--api-url=")
    two_word_flags+=("--api-url")
    flags+=("--handle=")
    two_word_flags+=("--handle")
    flags+=("--jq=")
    two_word_flags+=("--jq")
    flags+=("--json")
    flags+=("--profile=")
    two_word_flags+=("--profile")
    flags+=("--quiet")
    flags+=("--token=")
    two_word_flags+=("--token")
    flags+=("--verbose")
    flags+=("-v")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_pura_mcp_install()
{
    last_command="pura_mcp_install"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--client=")
    two_word_flags+=("--client")
    local_nonpersistent_flags+=("--client")
    local_nonpersistent_flags+=("--client=")
    flags+=("--name=")
    two_word_flags+=("--name")
    local_nonpersistent_flags+=("--name")
    local_nonpersistent_flags+=("--name=")
    flags+=("--permissions=")
    two_word_flags+=("--permissions")
    local_nonpersistent_flags+=("--permissions")
    local_nonpersistent_flags+=("--permissions=")
    flags+=("--print")
    local_nonpersistent_flags+=("--print")
    flags+=("--scope=")
    two_word_flags+=("--scope")
    local_nonpersistent_flags+=("--scope")
    local_nonpersistent_flags+=("--scope=")
    flags+=("--transport=")
    two_word_flags+=("--transport")
    local_nonpersistent_flags+=("--transport")
    local_nonpersistent_flags+=("--transport=")
    flags+=("--yes")
    flags+=("-y")
    local_nonpersistent_flags+=("--yes")
    local_nonpersistent_flags+=("-y")
    flags+=("--api-url=")
    two_word_flags+=("--api-url")
    flags+=("--handle=")
    two_word_flags+=("--handle")
    flags+=("--jq=")
    two_word_flags+=("--jq")
    flags+=("--json")
    flags+=("--profile=")
    two_word_flags+=("--profile")
    flags+=("--quiet")
    flags+=("--token=")
    two_word_flags+=("--token")
    flags+=("--verbose")
    flags+=("-v")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_pura_mcp_ls()
{
    last_command="pura_mcp_ls"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--all-keys")
    local_nonpersistent_flags+=("--all-keys")
    flags+=("--scope=")
    two_word_flags+=("--scope")
    local_nonpersistent_flags+=("--scope")
    local_nonpersistent_flags+=("--scope=")
    flags+=("--api-url=")
    two_word_flags+=("--api-url")
    flags+=("--handle=")
    two_word_flags+=("--handle")
    flags+=("--jq=")
    two_word_flags+=("--jq")
    flags+=("--json")
    flags+=("--profile=")
    two_word_flags+=("--profile")
    flags+=("--quiet")
    flags+=("--token=")
    two_word_flags+=("--token")
    flags+=("--verbose")
    flags+=("-v")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_pura_mcp_rotate()
{
    last_command="pura_mcp_rotate"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--client=")
    two_word_flags+=("--client")
    local_nonpersistent_flags+=("--client")
    local_nonpersistent_flags+=("--client=")
    flags+=("--scope=")
    two_word_flags+=("--scope")
    local_nonpersistent_flags+=("--scope")
    local_nonpersistent_flags+=("--scope=")
    flags+=("--api-url=")
    two_word_flags+=("--api-url")
    flags+=("--handle=")
    two_word_flags+=("--handle")
    flags+=("--jq=")
    two_word_flags+=("--jq")
    flags+=("--json")
    flags+=("--profile=")
    two_word_flags+=("--profile")
    flags+=("--quiet")
    flags+=("--token=")
    two_word_flags+=("--token")
    flags+=("--verbose")
    flags+=("-v")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_pura_mcp_test()
{
    last_command="pura_mcp_test"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--client=")
    two_word_flags+=("--client")
    local_nonpersistent_flags+=("--client")
    local_nonpersistent_flags+=("--client=")
    flags+=("--scope=")
    two_word_flags+=("--scope")
    local_nonpersistent_flags+=("--scope")
    local_nonpersistent_flags+=("--scope=")
    flags+=("--url=")
    two_word_flags+=("--url")
    local_nonpersistent_flags+=("--url")
    local_nonpersistent_flags+=("--url=")
    flags+=("--api-url=")
    two_word_flags+=("--api-url")
    flags+=("--handle=")
    two_word_flags+=("--handle")
    flags+=("--jq=")
    two_word_flags+=("--jq")
    flags+=("--json")
    flags+=("--profile=")
    two_word_flags+=("--profile")
    flags+=("--quiet")
    flags+=("--token=")
    two_word_flags+=("--token")
    flags+=("--verbose")
    flags+=("-v")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_pura_mcp_uninstall()
{
    last_command="pura_mcp_uninstall"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--client=")
    two_word_flags+=("--client")
    local_nonpersistent_flags+=("--client")
    local_nonpersistent_flags+=("--client=")
    flags+=("--keep-key")
    local_nonpersistent_flags+=("--keep-key")
    flags+=("--scope=")
    two_word_flags+=("--scope")
    local_nonpersistent_flags+=("--scope")
    local_nonpersistent_flags+=("--scope=")
    flags+=("--api-url=")
    two_word_flags+=("--api-url")
    flags+=("--handle=")
    two_word_flags+=("--handle")
    flags+=("--jq=")
    two_word_flags+=("--jq")
    flags+=("--json")
    flags+=("--profile=")
    two_word_flags+=("--profile")
    flags+=("--quiet")
    flags+=("--token=")
    two_word_flags+=("--token")
    flags+=("--verbose")
    flags+=("-v")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_pura_mcp()
{
    last_command="pura_mcp"

    command_aliases=()

    commands=()
    commands+=("config")
    commands+=("doctor")
    commands+=("install")
    commands+=("ls")
    commands+=("rotate")
    commands+=("test")
    commands+=("uninstall")

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--api-url=")
    two_word_flags+=("--api-url")
    flags+=("--handle=")
    two_word_flags+=("--handle")
    flags+=("--jq=")
    two_word_flags+=("--jq")
    flags+=("--json")
    flags+=("--profile=")
    two_word_flags+=("--profile")
    flags+=("--quiet")
    flags+=("--token=")
    two_word_flags+=("--token")
    flags+=("--verbose")
    flags+=("-v")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_pura_new()
{
    last_command="pura_new"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--describe=")
    two_word_flags+=("--describe")
    local_nonpersistent_flags+=("--describe")
    local_nonpersistent_flags+=("--describe=")
    flags+=("--model=")
    two_word_flags+=("--model")
    local_nonpersistent_flags+=("--model")
    local_nonpersistent_flags+=("--model=")
    flags+=("--open")
    local_nonpersistent_flags+=("--open")
    flags+=("--starter=")
    two_word_flags+=("--starter")
    local_nonpersistent_flags+=("--starter")
    local_nonpersistent_flags+=("--starter=")
    flags+=("--yes")
    local_nonpersistent_flags+=("--yes")
    flags+=("--api-url=")
    two_word_flags+=("--api-url")
    flags+=("--handle=")
    two_word_flags+=("--handle")
    flags+=("--jq=")
    two_word_flags+=("--jq")
    flags+=("--json")
    flags+=("--profile=")
    two_word_flags+=("--profile")
    flags+=("--quiet")
    flags+=("--token=")
    two_word_flags+=("--token")
    flags+=("--verbose")
    flags+=("-v")

    must_have_one_flag=()
    must_have_one_flag+=("--describe=")
    must_have_one_noun=()
    noun_aliases=()
}

_pura_open()
{
    last_command="pura_open"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--api-url=")
    two_word_flags+=("--api-url")
    flags+=("--handle=")
    two_word_flags+=("--handle")
    flags+=("--jq=")
    two_word_flags+=("--jq")
    flags+=("--json")
    flags+=("--profile=")
    two_word_flags+=("--profile")
    flags+=("--quiet")
    flags+=("--token=")
    two_word_flags+=("--token")
    flags+=("--verbose")
    flags+=("-v")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_pura_present()
{
    last_command="pura_present"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--api-url=")
    two_word_flags+=("--api-url")
    flags+=("--handle=")
    two_word_flags+=("--handle")
    flags+=("--jq=")
    two_word_flags+=("--jq")
    flags+=("--json")
    flags+=("--profile=")
    two_word_flags+=("--profile")
    flags+=("--quiet")
    flags+=("--token=")
    two_word_flags+=("--token")
    flags+=("--verbose")
    flags+=("-v")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_pura_preview()
{
    last_command="pura_preview"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--api-url=")
    two_word_flags+=("--api-url")
    flags+=("--handle=")
    two_word_flags+=("--handle")
    flags+=("--jq=")
    two_word_flags+=("--jq")
    flags+=("--json")
    flags+=("--profile=")
    two_word_flags+=("--profile")
    flags+=("--quiet")
    flags+=("--token=")
    two_word_flags+=("--token")
    flags+=("--verbose")
    flags+=("-v")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_pura_push()
{
    last_command="pura_push"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--kind=")
    two_word_flags+=("--kind")
    local_nonpersistent_flags+=("--kind")
    local_nonpersistent_flags+=("--kind=")
    flags+=("--open")
    flags+=("-o")
    local_nonpersistent_flags+=("--open")
    local_nonpersistent_flags+=("-o")
    flags+=("--stdin")
    local_nonpersistent_flags+=("--stdin")
    flags+=("--substrate=")
    two_word_flags+=("--substrate")
    local_nonpersistent_flags+=("--substrate")
    local_nonpersistent_flags+=("--substrate=")
    flags+=("--theme=")
    two_word_flags+=("--theme")
    local_nonpersistent_flags+=("--theme")
    local_nonpersistent_flags+=("--theme=")
    flags+=("--title=")
    two_word_flags+=("--title")
    two_word_flags+=("-t")
    local_nonpersistent_flags+=("--title")
    local_nonpersistent_flags+=("--title=")
    local_nonpersistent_flags+=("-t")
    flags+=("--api-url=")
    two_word_flags+=("--api-url")
    flags+=("--handle=")
    two_word_flags+=("--handle")
    flags+=("--jq=")
    two_word_flags+=("--jq")
    flags+=("--json")
    flags+=("--profile=")
    two_word_flags+=("--profile")
    flags+=("--quiet")
    flags+=("--token=")
    two_word_flags+=("--token")
    flags+=("--verbose")
    flags+=("-v")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_pura_rm()
{
    last_command="pura_rm"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--yes")
    flags+=("-y")
    local_nonpersistent_flags+=("--yes")
    local_nonpersistent_flags+=("-y")
    flags+=("--api-url=")
    two_word_flags+=("--api-url")
    flags+=("--handle=")
    two_word_flags+=("--handle")
    flags+=("--jq=")
    two_word_flags+=("--jq")
    flags+=("--json")
    flags+=("--profile=")
    two_word_flags+=("--profile")
    flags+=("--quiet")
    flags+=("--token=")
    two_word_flags+=("--token")
    flags+=("--verbose")
    flags+=("-v")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_pura_sheet_clone()
{
    last_command="pura_sheet_clone"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--to=")
    two_word_flags+=("--to")
    local_nonpersistent_flags+=("--to")
    local_nonpersistent_flags+=("--to=")
    flags+=("--to-handle=")
    two_word_flags+=("--to-handle")
    local_nonpersistent_flags+=("--to-handle")
    local_nonpersistent_flags+=("--to-handle=")
    flags+=("--api-url=")
    two_word_flags+=("--api-url")
    flags+=("--handle=")
    two_word_flags+=("--handle")
    flags+=("--jq=")
    two_word_flags+=("--jq")
    flags+=("--json")
    flags+=("--profile=")
    two_word_flags+=("--profile")
    flags+=("--quiet")
    flags+=("--token=")
    two_word_flags+=("--token")
    flags+=("--verbose")
    flags+=("-v")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_pura_sheet_export()
{
    last_command="pura_sheet_export"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--format=")
    two_word_flags+=("--format")
    local_nonpersistent_flags+=("--format")
    local_nonpersistent_flags+=("--format=")
    flags+=("--out=")
    two_word_flags+=("--out")
    local_nonpersistent_flags+=("--out")
    local_nonpersistent_flags+=("--out=")
    flags+=("--api-url=")
    two_word_flags+=("--api-url")
    flags+=("--handle=")
    two_word_flags+=("--handle")
    flags+=("--jq=")
    two_word_flags+=("--jq")
    flags+=("--json")
    flags+=("--profile=")
    two_word_flags+=("--profile")
    flags+=("--quiet")
    flags+=("--token=")
    two_word_flags+=("--token")
    flags+=("--verbose")
    flags+=("-v")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_pura_sheet_ls()
{
    last_command="pura_sheet_ls"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--cursor=")
    two_word_flags+=("--cursor")
    local_nonpersistent_flags+=("--cursor")
    local_nonpersistent_flags+=("--cursor=")
    flags+=("--limit=")
    two_word_flags+=("--limit")
    local_nonpersistent_flags+=("--limit")
    local_nonpersistent_flags+=("--limit=")
    flags+=("--api-url=")
    two_word_flags+=("--api-url")
    flags+=("--handle=")
    two_word_flags+=("--handle")
    flags+=("--jq=")
    two_word_flags+=("--jq")
    flags+=("--json")
    flags+=("--profile=")
    two_word_flags+=("--profile")
    flags+=("--quiet")
    flags+=("--token=")
    two_word_flags+=("--token")
    flags+=("--verbose")
    flags+=("-v")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_pura_sheet_schema()
{
    last_command="pura_sheet_schema"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--api-url=")
    two_word_flags+=("--api-url")
    flags+=("--handle=")
    two_word_flags+=("--handle")
    flags+=("--jq=")
    two_word_flags+=("--jq")
    flags+=("--json")
    flags+=("--profile=")
    two_word_flags+=("--profile")
    flags+=("--quiet")
    flags+=("--token=")
    two_word_flags+=("--token")
    flags+=("--verbose")
    flags+=("-v")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_pura_sheet()
{
    last_command="pura_sheet"

    command_aliases=()

    commands=()
    commands+=("clone")
    commands+=("export")
    commands+=("ls")
    commands+=("schema")

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--api-url=")
    two_word_flags+=("--api-url")
    flags+=("--handle=")
    two_word_flags+=("--handle")
    flags+=("--jq=")
    two_word_flags+=("--jq")
    flags+=("--json")
    flags+=("--profile=")
    two_word_flags+=("--profile")
    flags+=("--quiet")
    flags+=("--token=")
    two_word_flags+=("--token")
    flags+=("--verbose")
    flags+=("-v")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_pura_skill_install()
{
    last_command="pura_skill_install"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--source=")
    two_word_flags+=("--source")
    local_nonpersistent_flags+=("--source")
    local_nonpersistent_flags+=("--source=")
    flags+=("--target=")
    two_word_flags+=("--target")
    local_nonpersistent_flags+=("--target")
    local_nonpersistent_flags+=("--target=")
    flags+=("--api-url=")
    two_word_flags+=("--api-url")
    flags+=("--handle=")
    two_word_flags+=("--handle")
    flags+=("--jq=")
    two_word_flags+=("--jq")
    flags+=("--json")
    flags+=("--profile=")
    two_word_flags+=("--profile")
    flags+=("--quiet")
    flags+=("--token=")
    two_word_flags+=("--token")
    flags+=("--verbose")
    flags+=("-v")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_pura_skill_ls()
{
    last_command="pura_skill_ls"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--api-url=")
    two_word_flags+=("--api-url")
    flags+=("--handle=")
    two_word_flags+=("--handle")
    flags+=("--jq=")
    two_word_flags+=("--jq")
    flags+=("--json")
    flags+=("--profile=")
    two_word_flags+=("--profile")
    flags+=("--quiet")
    flags+=("--token=")
    two_word_flags+=("--token")
    flags+=("--verbose")
    flags+=("-v")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_pura_skill_rm()
{
    last_command="pura_skill_rm"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--target=")
    two_word_flags+=("--target")
    local_nonpersistent_flags+=("--target")
    local_nonpersistent_flags+=("--target=")
    flags+=("--api-url=")
    two_word_flags+=("--api-url")
    flags+=("--handle=")
    two_word_flags+=("--handle")
    flags+=("--jq=")
    two_word_flags+=("--jq")
    flags+=("--json")
    flags+=("--profile=")
    two_word_flags+=("--profile")
    flags+=("--quiet")
    flags+=("--token=")
    two_word_flags+=("--token")
    flags+=("--verbose")
    flags+=("-v")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_pura_skill_run()
{
    last_command="pura_skill_run"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--api-url=")
    two_word_flags+=("--api-url")
    flags+=("--handle=")
    two_word_flags+=("--handle")
    flags+=("--jq=")
    two_word_flags+=("--jq")
    flags+=("--json")
    flags+=("--profile=")
    two_word_flags+=("--profile")
    flags+=("--quiet")
    flags+=("--token=")
    two_word_flags+=("--token")
    flags+=("--verbose")
    flags+=("-v")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_pura_skill()
{
    last_command="pura_skill"

    command_aliases=()

    commands=()
    commands+=("install")
    commands+=("ls")
    if [[ -z "${BASH_VERSION:-}" || "${BASH_VERSINFO[0]:-}" -gt 3 ]]; then
        command_aliases+=("list")
        aliashash["list"]="ls"
    fi
    commands+=("rm")
    if [[ -z "${BASH_VERSION:-}" || "${BASH_VERSINFO[0]:-}" -gt 3 ]]; then
        command_aliases+=("remove")
        aliashash["remove"]="rm"
    fi
    commands+=("run")

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--api-url=")
    two_word_flags+=("--api-url")
    flags+=("--handle=")
    two_word_flags+=("--handle")
    flags+=("--jq=")
    two_word_flags+=("--jq")
    flags+=("--json")
    flags+=("--profile=")
    two_word_flags+=("--profile")
    flags+=("--quiet")
    flags+=("--token=")
    two_word_flags+=("--token")
    flags+=("--verbose")
    flags+=("-v")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_pura_stats()
{
    last_command="pura_stats"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--detail")
    local_nonpersistent_flags+=("--detail")
    flags+=("--api-url=")
    two_word_flags+=("--api-url")
    flags+=("--handle=")
    two_word_flags+=("--handle")
    flags+=("--jq=")
    two_word_flags+=("--jq")
    flags+=("--json")
    flags+=("--profile=")
    two_word_flags+=("--profile")
    flags+=("--quiet")
    flags+=("--token=")
    two_word_flags+=("--token")
    flags+=("--verbose")
    flags+=("-v")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_pura_tool_call()
{
    last_command="pura_tool_call"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--args=")
    two_word_flags+=("--args")
    local_nonpersistent_flags+=("--args")
    local_nonpersistent_flags+=("--args=")
    flags+=("--dry-run")
    local_nonpersistent_flags+=("--dry-run")
    flags+=("--idempotency-key=")
    two_word_flags+=("--idempotency-key")
    local_nonpersistent_flags+=("--idempotency-key")
    local_nonpersistent_flags+=("--idempotency-key=")
    flags+=("--api-url=")
    two_word_flags+=("--api-url")
    flags+=("--handle=")
    two_word_flags+=("--handle")
    flags+=("--jq=")
    two_word_flags+=("--jq")
    flags+=("--json")
    flags+=("--profile=")
    two_word_flags+=("--profile")
    flags+=("--quiet")
    flags+=("--token=")
    two_word_flags+=("--token")
    flags+=("--verbose")
    flags+=("-v")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_pura_tool_inspect()
{
    last_command="pura_tool_inspect"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--refresh")
    local_nonpersistent_flags+=("--refresh")
    flags+=("--api-url=")
    two_word_flags+=("--api-url")
    flags+=("--handle=")
    two_word_flags+=("--handle")
    flags+=("--jq=")
    two_word_flags+=("--jq")
    flags+=("--json")
    flags+=("--profile=")
    two_word_flags+=("--profile")
    flags+=("--quiet")
    flags+=("--token=")
    two_word_flags+=("--token")
    flags+=("--verbose")
    flags+=("-v")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_pura_tool_ls()
{
    last_command="pura_tool_ls"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--refresh")
    local_nonpersistent_flags+=("--refresh")
    flags+=("--api-url=")
    two_word_flags+=("--api-url")
    flags+=("--handle=")
    two_word_flags+=("--handle")
    flags+=("--jq=")
    two_word_flags+=("--jq")
    flags+=("--json")
    flags+=("--profile=")
    two_word_flags+=("--profile")
    flags+=("--quiet")
    flags+=("--token=")
    two_word_flags+=("--token")
    flags+=("--verbose")
    flags+=("-v")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_pura_tool()
{
    last_command="pura_tool"

    command_aliases=()

    commands=()
    commands+=("call")
    commands+=("inspect")
    commands+=("ls")

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--api-url=")
    two_word_flags+=("--api-url")
    flags+=("--handle=")
    two_word_flags+=("--handle")
    flags+=("--jq=")
    two_word_flags+=("--jq")
    flags+=("--json")
    flags+=("--profile=")
    two_word_flags+=("--profile")
    flags+=("--quiet")
    flags+=("--token=")
    two_word_flags+=("--token")
    flags+=("--verbose")
    flags+=("-v")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_pura_version()
{
    last_command="pura_version"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--api-url=")
    two_word_flags+=("--api-url")
    flags+=("--handle=")
    two_word_flags+=("--handle")
    flags+=("--jq=")
    two_word_flags+=("--jq")
    flags+=("--json")
    flags+=("--profile=")
    two_word_flags+=("--profile")
    flags+=("--quiet")
    flags+=("--token=")
    two_word_flags+=("--token")
    flags+=("--verbose")
    flags+=("-v")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_pura_versions_diff()
{
    last_command="pura_versions_diff"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--color=")
    two_word_flags+=("--color")
    local_nonpersistent_flags+=("--color")
    local_nonpersistent_flags+=("--color=")
    flags+=("--api-url=")
    two_word_flags+=("--api-url")
    flags+=("--handle=")
    two_word_flags+=("--handle")
    flags+=("--jq=")
    two_word_flags+=("--jq")
    flags+=("--json")
    flags+=("--profile=")
    two_word_flags+=("--profile")
    flags+=("--quiet")
    flags+=("--token=")
    two_word_flags+=("--token")
    flags+=("--verbose")
    flags+=("-v")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_pura_versions_ls()
{
    last_command="pura_versions_ls"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--api-url=")
    two_word_flags+=("--api-url")
    flags+=("--handle=")
    two_word_flags+=("--handle")
    flags+=("--jq=")
    two_word_flags+=("--jq")
    flags+=("--json")
    flags+=("--profile=")
    two_word_flags+=("--profile")
    flags+=("--quiet")
    flags+=("--token=")
    two_word_flags+=("--token")
    flags+=("--verbose")
    flags+=("-v")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_pura_versions_restore()
{
    last_command="pura_versions_restore"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--yes")
    local_nonpersistent_flags+=("--yes")
    flags+=("--api-url=")
    two_word_flags+=("--api-url")
    flags+=("--handle=")
    two_word_flags+=("--handle")
    flags+=("--jq=")
    two_word_flags+=("--jq")
    flags+=("--json")
    flags+=("--profile=")
    two_word_flags+=("--profile")
    flags+=("--quiet")
    flags+=("--token=")
    two_word_flags+=("--token")
    flags+=("--verbose")
    flags+=("-v")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_pura_versions_show()
{
    last_command="pura_versions_show"

    command_aliases=()

    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--api-url=")
    two_word_flags+=("--api-url")
    flags+=("--handle=")
    two_word_flags+=("--handle")
    flags+=("--jq=")
    two_word_flags+=("--jq")
    flags+=("--json")
    flags+=("--profile=")
    two_word_flags+=("--profile")
    flags+=("--quiet")
    flags+=("--token=")
    two_word_flags+=("--token")
    flags+=("--verbose")
    flags+=("-v")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_pura_versions()
{
    last_command="pura_versions"

    command_aliases=()

    commands=()
    commands+=("diff")
    commands+=("ls")
    commands+=("restore")
    commands+=("show")

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--api-url=")
    two_word_flags+=("--api-url")
    flags+=("--handle=")
    two_word_flags+=("--handle")
    flags+=("--jq=")
    two_word_flags+=("--jq")
    flags+=("--json")
    flags+=("--profile=")
    two_word_flags+=("--profile")
    flags+=("--quiet")
    flags+=("--token=")
    two_word_flags+=("--token")
    flags+=("--verbose")
    flags+=("-v")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_pura_root_command()
{
    last_command="pura"

    command_aliases=()

    commands=()
    commands+=("auth")
    commands+=("book")
    commands+=("chat")
    commands+=("claim")
    commands+=("completion")
    commands+=("config")
    commands+=("doctor")
    commands+=("edit")
    commands+=("events")
    commands+=("get")
    commands+=("help")
    commands+=("keys")
    commands+=("ls")
    if [[ -z "${BASH_VERSION:-}" || "${BASH_VERSINFO[0]:-}" -gt 3 ]]; then
        command_aliases+=("list")
        aliashash["list"]="ls"
    fi
    commands+=("mcp")
    commands+=("new")
    commands+=("open")
    commands+=("present")
    commands+=("preview")
    commands+=("push")
    commands+=("rm")
    commands+=("sheet")
    commands+=("skill")
    commands+=("stats")
    commands+=("tool")
    commands+=("version")
    commands+=("versions")

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--api-url=")
    two_word_flags+=("--api-url")
    flags+=("--handle=")
    two_word_flags+=("--handle")
    flags+=("--jq=")
    two_word_flags+=("--jq")
    flags+=("--json")
    flags+=("--profile=")
    two_word_flags+=("--profile")
    flags+=("--quiet")
    flags+=("--token=")
    two_word_flags+=("--token")
    flags+=("--verbose")
    flags+=("-v")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

__start_pura()
{
    local cur prev words cword split
    declare -A flaghash 2>/dev/null || :
    declare -A aliashash 2>/dev/null || :
    if declare -F _init_completion >/dev/null 2>&1; then
        _init_completion -s || return
    else
        __pura_init_completion -n "=" || return
    fi

    local c=0
    local flag_parsing_disabled=
    local flags=()
    local two_word_flags=()
    local local_nonpersistent_flags=()
    local flags_with_completion=()
    local flags_completion=()
    local commands=("pura")
    local command_aliases=()
    local must_have_one_flag=()
    local must_have_one_noun=()
    local has_completion_function=""
    local last_command=""
    local nouns=()
    local noun_aliases=()

    __pura_handle_word
}

if [[ $(type -t compopt) = "builtin" ]]; then
    complete -o default -F __start_pura pura
else
    complete -o default -o nospace -F __start_pura pura
fi

# ex: ts=4 sw=4 et filetype=sh
