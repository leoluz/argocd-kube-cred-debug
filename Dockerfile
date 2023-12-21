# Builder image
FROM golang:1.21.3 as builder

WORKDIR /workspace
# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
RUN go mod download
COPY cmd cmd

RUN CGO_ENABLED=0 go build -o argocd-kube-cred-debug ./cmd/argocd-kube-cred-debug
RUN CGO_ENABLED=0 go build -o argocd-k8s-auth ./cmd/argocd-k8s-auth

# Main image
FROM alpine:latest

WORKDIR /app

COPY --from=builder /workspace/argocd-kube-cred-debug .
COPY --from=builder /workspace/argocd-k8s-auth /usr/local/bin

ENTRYPOINT ["/app/argocd-kube-cred-debug"]
