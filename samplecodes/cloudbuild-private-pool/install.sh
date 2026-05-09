#!/usr/bin/env bash
# Install dev tooling on Ubuntu 24.04: Go, Terraform, gcloud, task, Node.js (npm).
# Idempotent: safe to re-run. Skips packages that are already present.
set -euo pipefail

SUDO="sudo"
if [[ $EUID -eq 0 ]]; then SUDO=""; fi

# Override with: GO_VERSION=go1.24.0 ./install.sh
GO_VERSION="${GO_VERSION:-}"
# Override with: NODE_MAJOR=22 ./install.sh
NODE_MAJOR="${NODE_MAJOR:-20}"

need_apt_update=0

install_terraform() {
  if command -v terraform >/dev/null 2>&1; then
    echo "[skip] terraform: $(terraform version | head -1)"
    return
  fi
  echo "[install] terraform"
  $SUDO install -m 0755 -d /etc/apt/keyrings
  curl -fsSL https://apt.releases.hashicorp.com/gpg \
    | $SUDO gpg --dearmor -o /etc/apt/keyrings/hashicorp-archive-keyring.gpg
  echo "deb [signed-by=/etc/apt/keyrings/hashicorp-archive-keyring.gpg] https://apt.releases.hashicorp.com $(lsb_release -cs) main" \
    | $SUDO tee /etc/apt/sources.list.d/hashicorp.list >/dev/null
  need_apt_update=1
}

install_go() {
  if command -v go >/dev/null 2>&1; then
    echo "[skip] go: $(go version)"
    return
  fi
  local version="$GO_VERSION"
  if [[ -z "$version" ]]; then
    version="$(curl -fsSL 'https://go.dev/VERSION?m=text' | head -1)"
  fi
  local arch
  case "$(uname -m)" in
    x86_64) arch=amd64 ;;
    aarch64|arm64) arch=arm64 ;;
    *) echo "unsupported arch: $(uname -m)"; exit 1 ;;
  esac
  local tarball="${version}.linux-${arch}.tar.gz"
  echo "[install] go ($version)"
  curl -fsSL -o "/tmp/${tarball}" "https://go.dev/dl/${tarball}"
  $SUDO rm -rf /usr/local/go
  $SUDO tar -C /usr/local -xzf "/tmp/${tarball}"
  rm -f "/tmp/${tarball}"
  # Make `go` available in current and future shells.
  $SUDO ln -sf /usr/local/go/bin/go /usr/local/bin/go
  $SUDO ln -sf /usr/local/go/bin/gofmt /usr/local/bin/gofmt
}

install_gcloud() {
  if command -v gcloud >/dev/null 2>&1; then
    echo "[skip] gcloud: $(gcloud version | head -1)"
    return
  fi
  echo "[install] google-cloud-cli"
  $SUDO install -m 0755 -d /etc/apt/keyrings
  curl -fsSL https://packages.cloud.google.com/apt/doc/apt-key.gpg \
    | $SUDO gpg --dearmor -o /etc/apt/keyrings/cloud.google.gpg
  echo "deb [signed-by=/etc/apt/keyrings/cloud.google.gpg] https://packages.cloud.google.com/apt cloud-sdk main" \
    | $SUDO tee /etc/apt/sources.list.d/google-cloud-sdk.list >/dev/null
  need_apt_update=1
}

install_node() {
  if command -v npm >/dev/null 2>&1 && command -v node >/dev/null 2>&1; then
    echo "[skip] node: $(node --version) / npm: $(npm --version)"
    return
  fi
  echo "[install] nodejs (NodeSource ${NODE_MAJOR}.x)"
  $SUDO install -m 0755 -d /etc/apt/keyrings
  curl -fsSL https://deb.nodesource.com/gpgkey/nodesource-repo.gpg.key \
    | $SUDO gpg --dearmor -o /etc/apt/keyrings/nodesource.gpg
  echo "deb [signed-by=/etc/apt/keyrings/nodesource.gpg] https://deb.nodesource.com/node_${NODE_MAJOR}.x nodistro main" \
    | $SUDO tee /etc/apt/sources.list.d/nodesource.list >/dev/null
  need_apt_update=1
}

install_task() {
  if command -v task >/dev/null 2>&1; then
    echo "[skip] task: $(task --version)"
    return
  fi
  if ! command -v go >/dev/null 2>&1; then
    echo "[error] go is required to install task"; exit 1
  fi
  echo "[install] task (via go install)"
  GOBIN="$(go env GOPATH)/bin" go install github.com/go-task/task/v3/cmd/task@latest
  $SUDO ln -sf "$(go env GOPATH)/bin/task" /usr/local/bin/task
}

$SUDO apt-get update -qq
$SUDO apt-get install -y -qq curl gnupg lsb-release ca-certificates apt-transport-https

install_go
install_terraform
install_gcloud
install_node

if [[ $need_apt_update -eq 1 ]]; then
  $SUDO apt-get update -qq
  pkgs=()
  command -v terraform >/dev/null 2>&1 || pkgs+=(terraform)
  command -v gcloud >/dev/null 2>&1 || pkgs+=(google-cloud-cli)
  { command -v node >/dev/null 2>&1 && command -v npm >/dev/null 2>&1; } || pkgs+=(nodejs)
  if [[ ${#pkgs[@]} -gt 0 ]]; then
    $SUDO apt-get install -y -qq "${pkgs[@]}"
  fi
fi

# task depends on go, so install it after the apt phase.
install_task

echo
echo "=== versions ==="
go version
terraform version | head -1
gcloud version | head -1
node --version
npm --version
task --version
