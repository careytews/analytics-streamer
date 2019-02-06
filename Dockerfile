FROM fedora:26

RUN dnf install -y libgo

COPY streamer /usr/local/bin/

ENTRYPOINT ["/usr/local/bin/streamer"]
CMD [ "/queue/input" ]

