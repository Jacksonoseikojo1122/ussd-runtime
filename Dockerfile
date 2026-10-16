FROM golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /ussd-runtime ./cmd/ussd-runtime

# Distroless: no shell, no package manager, runs as non-root.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /ussd-runtime /ussd-runtime
COPY flows /flows
ENV PORT=8080 FLOW_FILE=/flows/demo-wallet.json
EXPOSE 8080
ENTRYPOINT ["/ussd-runtime"]
