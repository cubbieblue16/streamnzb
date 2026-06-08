ARG TARGETARCH

FROM alpine:latest
# par2cmdline provides the `par2` binary used by the optional (default-off) PAR2
# download-repair-serve fallback (pkg/services/par2).
RUN apk add --no-cache ca-certificates tzdata par2cmdline
WORKDIR /app

ARG TARGETARCH
# Copy the pre-built binary based on the target architecture
COPY dist/linux_${TARGETARCH}/streamnzb .

EXPOSE 7000
EXPOSE 119
CMD ["./streamnzb"]
