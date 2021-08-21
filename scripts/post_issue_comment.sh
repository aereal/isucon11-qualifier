#!/bin/bash
set -ue

if [[ $# == 0 ]]; then
    exec gh issue comment 3 -R aereal/isucon11-qualifier --body-file /dev/stdin
    exit
fi

(
    echo '<details>'
    echo
    echo "<summary>$*</summary>"
    echo
    echo '```'
    "$@"
    echo '```'
    echo
    echo '</details>'
) | $0
