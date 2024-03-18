#!/bin/bash

# constants difinition
REPO_NAME="avro-schema-go"
GO_PACKAGE="message"
TARGET_DIR="../${REPO_NAME}/"
MODNAME="example.com/${REPO_NAME}"

# clean output directory
rm -r  ${TARGET_DIR}
mkdir -p  ${TARGET_DIR}/${GO_PACKAGE}

# generate avro schema written in golang
gogen-avro -package ${GO_PACKAGE}  ${TARGET_DIR}/${GO_PACKAGE} ./schemas/*.avsc

# generate go.mod and go.sum
cd ${TARGET_DIR}
go mod init ${MODNAME}
go mod tidy
