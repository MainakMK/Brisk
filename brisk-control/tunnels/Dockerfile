# Brisk laptop-side reverse-SSH tunnel (Phase 3.7 Step 1, connectivity Option A).
#
# Runs autossh in a container ON THE LAPTOP. It dials OUTBOUND to each public edge
# (laptop->edge is NAT-friendly) and sets up reverse forwards so each edge gets:
#   edge 127.0.0.1:18080 -> control plane (brisk-control:8080)
#   edge 127.0.0.1:14222 -> NATS         (nats:4222)
# The agent on each edge then points BRISK_CONTROL_URL/BRISK_NATS_URL at those
# localhost ports. Nothing is installed on the laptop host or the edges (sshd
# already exists); the control plane is never exposed publicly.
FROM alpine:3.20
RUN apk add --no-cache autossh openssh-client sshpass
COPY tunnel.sh /usr/local/bin/tunnel.sh
RUN chmod +x /usr/local/bin/tunnel.sh
ENTRYPOINT ["/usr/local/bin/tunnel.sh"]
