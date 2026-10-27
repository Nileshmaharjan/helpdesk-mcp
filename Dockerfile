FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /helpdesk-mcp ./cmd/helpdesk-mcp

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /helpdesk-mcp /helpdesk-mcp
EXPOSE 8080
ENTRYPOINT ["/helpdesk-mcp"]
CMD ["serve", "-transport", "http", "-addr", ":8080"]
