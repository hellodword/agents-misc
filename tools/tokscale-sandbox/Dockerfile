# syntax=docker/dockerfile:1
FROM node:22-alpine

RUN npm install -g tokscale@4.23.0 --ignore-scripts \
    && npm cache clean --force \
    && mkdir -p \
      /opt/tokscale-config \
      /mnt/antigravity-cli/conversations \
      /mnt/codex/sessions \
      /mnt/codex/archived_sessions \
      /mnt/claude/projects \
      /mnt/claude/transcripts \
      /mnt/gemini/tmp \
      /mnt/opencode/storage/message \
      /mnt/opencode/db \
    && touch \
      /mnt/opencode/db/opencode.db \
      /mnt/opencode/db/opencode.db-wal \
      /mnt/opencode/db/opencode.db-shm \
      /mnt/opencode/db/opencode-stable.db \
      /mnt/opencode/db/opencode-stable.db-wal \
      /mnt/opencode/db/opencode-stable.db-shm \
    && TOKSCALE_CONFIG_DIR=/opt/tokscale-config tokscale pricing refresh --no-spinner \
    && chmod -R a+rX /opt/tokscale-config /mnt

COPY --chmod=755 entrypoint.sh /entrypoint.sh

ENTRYPOINT ["/entrypoint.sh"]
