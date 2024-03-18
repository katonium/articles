

# 
# https://zenn.dev/zenn/articles/connect-to-github
# 
install() {
    npm init --yes
    npm install zenn-cli
    npx zenn init
}

update () {
    npm install zenn-cli@latest
}

# just cheatsheet for zenn-cli
create-article() {
    SLUG=$1
    echo "create-article [$SLUG]"
    # https://zenn.dev/zenn/articles/zenn-cli-guide
    npx zenn new:article --slug ${SLUG}
}

preview() {
    PORT="8000"
    echo "lanunch preview server with ${PORT} port"
    npx zenn preview --port 8000 --open
}

"$@"
