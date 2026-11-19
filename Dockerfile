# Servidor de sinalização do VeloDesk.
#   docker build -t velodesk-server .
FROM golang:1.27.4-alpine AS build
WORKDIR /src
COPY core/go.mod core/go.sum ./
RUN go mod download
COPY core/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/velodesk ./cmd/velodesk

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/velodesk /velodesk
EXPOSE 21116
ENTRYPOINT ["/velodesk", "server"]
CMD ["-addr", ":21116", "-updates", "/updates"]
