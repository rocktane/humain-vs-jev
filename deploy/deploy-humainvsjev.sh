#!/bin/bash
# Installé sur le serveur dans /usr/local/bin/deploy-humainvsjev.
# Seule commande autorisée pour la clé SSH de GitHub Actions (command="..." dans
# authorized_keys) : reçoit le binaire sur l'entrée standard, le remplace, redémarre
# le service et revient à la version précédente si le service ne répond pas.
set -euo pipefail

dir=/opt/humainvsjev
bin=$dir/humainvsjev
prev=$dir/humainvsjev.prev
service=humainvsjev
health_url=http://127.0.0.1:8090/api/config
max_bytes=$((50 * 1024 * 1024))

exec 9>/run/deploy-humainvsjev.lock
flock -n 9 || { echo "un déploiement est déjà en cours" >&2; exit 1; }

new=$(mktemp "$dir/.humainvsjev.XXXXXX")
trap 'rm -f "$new"' EXIT

head -c "$((max_bytes + 1))" >"$new"
size=$(stat -c %s "$new")
if [ "$size" -gt "$max_bytes" ]; then
	echo "binaire refusé : plus de $max_bytes octets" >&2
	exit 1
fi
if [ "$size" -lt 1000000 ]; then
	echo "binaire refusé : $size octets seulement" >&2
	exit 1
fi
if [ "$(head -c 4 "$new" | od -An -tx1 | tr -d ' \n')" != 7f454c46 ]; then
	echo "binaire refusé : ce n'est pas un exécutable ELF" >&2
	exit 1
fi

chown root:humainvsjev "$new"
chmod 750 "$new"
cp -p "$bin" "$prev"
mv -f "$new" "$bin"
systemctl restart "$service"

healthy() {
	for _ in $(seq 1 60); do
		if systemctl is-active --quiet "$service" && curl -fsS -o /dev/null --max-time 2 "$health_url" 2>/dev/null; then
			return 0
		fi
		sleep 0.5
	done
	return 1
}

if healthy; then
	echo "déployé : $size octets, $(sha256sum "$bin" | cut -c1-12)"
	exit 0
fi

echo "le service ne répond pas, retour à la version précédente" >&2
journalctl -u "$service" -n 20 --no-pager >&2 || true
cp -p "$prev" "$bin"
systemctl restart "$service"
if healthy; then
	echo "version précédente rétablie" >&2
else
	echo "ATTENTION : la version précédente ne répond pas non plus" >&2
fi
exit 1
