BINARY_NAME=gopostgres
BUILD_DIR=bin

.PHONY: build run clean

build:
	go build -o ${BUILD_DIR}/${BINARY_NAME} ./cmd/gopostgres

run: build 
	./${BUILD_DIR}/${BINARY_NAME}

clean:
	rm -rf ${BUILD_DIR}

