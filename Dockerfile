FROM golang:1.22-alpine as builder

WORKDIR /workspace
COPY go.mod go.sum ./
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

COPY . .

# Build
RUN CGO_ENABLED=0 GO111MODULE=on go build -a -installsuffix nocgo -o /diskinfo

FROM gcr.io/distroless/static:nonroot

WORKDIR /
COPY --from=builder /diskinfo ./
USER 65532:65532

ENTRYPOINT ["./diskinfo"]
