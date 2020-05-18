FROM fedora:31
LABEL maintainer="Matej Bizjak <matejb96@gmail.com>"

RUN dnf upgrade -y && \
    rpm -ivh "https://download1.rpmfusion.org/free/fedora/rpmfusion-free-release-31.noarch.rpm" && \
    dnf upgrade -y && \
    dnf install -y vlc-devel && \
    dnf clean all

COPY build/stream_stats_exporter /bin/stream_stats_exporter

ENTRYPOINT ["/bin/stream_stats_exporter"]
EXPOSE     8080