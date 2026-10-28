FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.work ./
COPY pkg/contracts ./pkg/contracts
COPY services/rca-engine ./services/rca-engine
COPY services/ingest-service/go.mod ./services/ingest-service/go.mod
COPY services/evidence-service/go.mod ./services/evidence-service/go.mod
COPY services/gateway-api/go.mod ./services/gateway-api/go.mod
COPY services/tilt-collector/go.mod ./services/tilt-collector/go.mod
RUN cd services/rca-engine && CGO_ENABLED=0 go build -o /out/rca ./cmd/rca

FROM alpine:3.20
WORKDIR /app
COPY --from=build /out/rca /app/rca
EXPOSE 8082
CMD ["/app/rca"]
