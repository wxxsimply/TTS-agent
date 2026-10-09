FROM golang:1.22-alpine AS builder
WORKDIR /src
ENV GOPROXY=https://goproxy.cn,direct
ENV GOSUMDB=sum.golang.google.cn
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -buildvcs=false -o /server ./cmd/server && \
    CGO_ENABLED=0 GOOS=linux go build -buildvcs=false -o /worker ./cmd/worker && \
    CGO_ENABLED=0 GOOS=linux go build -buildvcs=false -o /eval ./cmd/eval

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=builder /server /app/server
COPY --from=builder /worker /app/worker
COPY --from=builder /eval /app/eval
COPY domains /app/domains
COPY tests/eval /app/tests/eval
COPY internal/store/migrations /app/internal/store/migrations
COPY scripts /app/scripts
ENV DOMAINS_PATH=/app/domains
EXPOSE 8000
CMD ["/app/server"]
