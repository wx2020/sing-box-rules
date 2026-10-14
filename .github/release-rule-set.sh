#!/bin/bash

set -e -o pipefail

if [ -z "$GITHUB_TOKEN" ]; then
    echo "Error: GITHUB_TOKEN is not set"
    exit 1
fi

REPO="${GITHUB_REPOSITORY:-wx2020/sing-box-rules}"
REMOTE_URL="https://x-access-token:${GITHUB_TOKEN}@github.com/${REPO}.git"
ROOT_DIR="$(pwd)"

echo "Publishing rule-set branches for repository: ${REPO}"

# 1. sing-geoip
if [ -d "${ROOT_DIR}/sing-geoip/rule-set" ]; then
    echo "Publishing rule-set-geoip branch..."
    (
        cd "${ROOT_DIR}/sing-geoip/rule-set"
        rm -rf .git
        git init
        git config --local user.email "41898282+github-actions[bot]@users.noreply.github.com"
        git config --local user.name "github-actions[bot]"
        git remote add origin "${REMOTE_URL}"
        git branch -M rule-set-geoip
        git add -A
        git commit -m "Update geoip rule-set ($(date -u +'%Y-%m-%d %H:%M:%S UTC'))"
        git push -f origin rule-set-geoip
    )
    echo "rule-set-geoip published successfully."
fi

# 2. sing-geosite
if [ -d "${ROOT_DIR}/sing-geosite/rule-set" ]; then
    echo "Publishing rule-set-geosite branch..."
    (
        cd "${ROOT_DIR}/sing-geosite/rule-set"
        rm -rf .git
        git init
        git config --local user.email "41898282+github-actions[bot]@users.noreply.github.com"
        git config --local user.name "github-actions[bot]"
        git remote add origin "${REMOTE_URL}"
        git branch -M rule-set-geosite
        git add -A
        git commit -m "Update geosite rule-set ($(date -u +'%Y-%m-%d %H:%M:%S UTC'))"
        git push -f origin rule-set-geosite
    )
    echo "rule-set-geosite published successfully."
fi

echo "All rule-set branches updated successfully."
