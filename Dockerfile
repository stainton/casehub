FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /casehub .

FROM alpine:3.22
RUN mkdir -p /app/data
WORKDIR /app
COPY --from=build /casehub /app/casehub
EXPOSE 8080
ENTRYPOINT ["/app/casehub"]
