
echo-server() {
    docker run -it -p 8080:8080 kicbase/echo-server:1.0
}

"$@"
