echo "[weaver generate]"
weaver generate
echo "[go run .]"
SERVICEWEAVER_CONFIG=weaver.toml go run .
