# syntax=docker/dockerfile:1.7
# Minimal Tor SOCKS5 proxy for the dev compose stack. Avoids depending on a
# third-party tor image so the build is reproducible.

FROM alpine:3.20
RUN apk add --no-cache tor
COPY torrc /etc/tor/torrc
RUN chown -R tor:tor /var/lib/tor /etc/tor
USER tor
EXPOSE 9050
CMD ["tor", "-f", "/etc/tor/torrc"]
