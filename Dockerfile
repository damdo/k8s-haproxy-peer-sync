ARG ARCH=amd64

FROM golang:1.17-alpine3.14 as builder
WORKDIR /go/src/app
COPY go.mod .
COPY go.sum .
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${ARCH} go build -a -ldflags='-s -w -extldflags "-static"' -o k8s-haproxy-peer-sync ./...

FROM alpine:3.14
WORKDIR /app
COPY --from=builder /go/src/app/k8s-haproxy-peer-sync /app/
