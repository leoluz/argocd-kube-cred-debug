# Builder image
FROM golang:1.21.3 as builder

WORKDIR /workspace
# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
RUN go mod download
COPY main.go main.go

RUN CGO_ENABLED=0 go build -o argocd-kube-cred-debug .

# Main image
FROM alpine:latest

WORKDIR /app

COPY --from=builder /workspace/argocd-kube-cred-debug .

ENTRYPOINT ["/app/argocd-kube-cred-debug"]
