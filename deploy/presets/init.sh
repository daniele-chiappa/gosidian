#!/bin/sh
# Prepares the gosidian presets: .env from .env.example the first time,
# then every key and token that is still empty, generated here. A value
# already set is never changed, so running it again is safe. The models
# (LLM_*, EMBED_*) are yours to set in .env.
set -eu
cd "$(dirname "$0")"

if [ ! -f .env ]; then
	cp .env.example .env
	echo "Created .env from .env.example."
fi
chmod 600 .env

rand_b64() { head -c "$1" /dev/urandom | base64 | tr -d '\n'; }
rand_hex() { head -c "$1" /dev/urandom | od -An -tx1 | tr -d ' \n'; }

# fill NAME VALUE: sets NAME in .env when it is missing or empty (a comment
# after an empty value counts as empty).
fill() {
	awk -v name="$1" -v value="$2" '
		BEGIN { done = 0 }
		index($0, name "=") == 1 {
			rest = substr($0, length(name) + 2)
			sub(/^[ \t]*(#.*)?$/, "", rest)
			if (rest == "" && !done) { print name "=" value; done = 1; next }
			done = 1
		}
		{ print }
		END { if (!done) print name "=" value }
	' .env >.env.tmp && mv .env.tmp .env
}

fill GOSIDIAN_SECRETS_KEY "$(rand_b64 32)"
for name in DOCLING_TOKEN IMAGES_TOKEN CHAT_TOKEN DATABASE_TOKEN STORAGE_TOKEN QDRANT_API_KEY; do
	fill "$name" "$(rand_hex 24)"
done
chmod 600 .env
echo "Keys and tokens are in .env."

mkdir -p vault state secrets
# The image runs as uid 65532: the vault and the state dir must be its.
if [ "$(stat -c %u vault 2>/dev/null || stat -f %u vault)" != 65532 ]; then
	if [ "$(id -u)" = 0 ]; then
		chown -R 65532:65532 vault state secrets
	else
		echo "Give the folders to the container's user, once:"
		echo "  sudo chown -R 65532:65532 vault state secrets"
	fi
fi

for v in LLM_URL LLM_MODEL EMBED_URL EMBED_MODEL EMBED_DIMS; do
	if ! grep -Eq "^$v=[^ #]" .env; then
		echo "For the rag preset, set $v in .env (examples in .env.example)."
	fi
done
