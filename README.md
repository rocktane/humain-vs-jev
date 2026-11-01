# Humain vs Jev : qui repère l’image IA ?

Un petit jeu web en cinq rounds. À chaque round, deux images : une vraie photo et une image générée par IA. Le joueur choisit celle qu’il pense être générée par IA, pendant que Jev, un modèle de [TypeSafe](https://typesafe.ai), analyse les deux mêmes images. Le verdict compare les deux regards.

Le tout tient dans un seul processus Go, sans dépendance externe : serveur `net/http`, interface en HTML, CSS et JavaScript natifs, embarqués dans le binaire.

Démo en ligne : https://humainvsjev.23o.dev

Projet expérimental : les estimations de Jev ne prouvent jamais l’origine d’une image, et cinq paires ne permettent aucun benchmark.

## Prérequis

- Go 1.23 ou supérieur.
- Une clé API TypeSafe ayant accès aux images (fonctionnalité expérimentale de Jev).
- Au moins cinq paires d’images dont vous possédez les droits. Aucune image n’est fournie dans ce dépôt.

## Démarrage

```sh
cp .env.example .env              # puis renseigner TYPESAFE_API_KEY
cp corpus/manifest.example.json corpus/manifest.json   # puis décrire vos paires
go run .
```

Ouvrir http://127.0.0.1:8080 et cliquer sur **Défier Jev**.

Sans clé ou sans manifeste, le serveur démarre quand même et l’accueil indique ce qui manque. Un manifeste invalide, lui, bloque le démarrage avec un message d’erreur.

## Configuration

Le serveur charge `.env` à la racine du projet. Les variables déjà définies dans le shell sont prioritaires.

| Variable | Défaut | Rôle |
|---|---|---|
| `TYPESAFE_API_KEY` | aucune | Clé API, gardée côté serveur |
| `TYPESAFE_MODEL` | `jev-1.14.0` | Modèle Jev appelé |
| `JEV_TIMEOUT` | `20s` | Délai maximal par analyse (1 min au plus) |
| `ADDR` | `127.0.0.1:8080` | Adresse d’écoute |
| `CORPUS_MANIFEST` | `corpus/manifest.json` | Manifeste des paires d’images |
| `STATS_FILE` | `stats.json` | Fichier des statistiques cumulées affichées sur l’accueil |
| `PUBLIC_HOST` | aucun | Nom public du site (`jeu.example.com`), plusieurs séparés par des virgules. Sans lui, seules les adresses locales sont acceptées |
| `TRUST_PROXY` | `0` | `1` derrière un reverse proxy local : l’adresse du visiteur et le HTTPS sont lus dans `X-Forwarded-For` et `X-Forwarded-Proto` |
| `GAMES_PER_HOUR` | `20` | Parties par heure et par adresse IP (IPv6 par /64), `0` sans limite |
| `DAILY_CALLS` | `1000` | Appels TypeSafe par jour (UTC), `0` sans limite |
| `MAX_CONCURRENT_CALLS` | `8` | Appels TypeSafe simultanés, toutes parties confondues, `0` sans limite |

La clé n’est jamais envoyée au navigateur. Ne placez aucun secret dans `web/` : ce dossier est incorporé au binaire.

Chaque round déclenche deux appels à TypeSafe, soit dix par partie complète. Ces appels peuvent être facturés.

Pour limiter la facture d’un serveur public :

- chaque round prend ses deux appels dans le budget du jour au moment où il commence, donc une partie abandonnée ne bloque rien. Une partie ne démarre que s’il reste de quoi la jouer entière ; sinon l’accueil annonce le quota atteint jusqu’au lendemain. Si le quota tombe en cours de partie, les rounds restants se jouent sans Jev. Le budget est en mémoire : un redémarrage le remet à zéro ;
- une même adresse IP ne peut lancer que `GAMES_PER_HOUR` parties par heure ;
- une partie remplacée par une nouvelle annule ses analyses en cours ;
- les requêtes dont l’en-tête `Host` n’est ni local ni dans `PUBLIC_HOST` sont refusées (421), ce qui bloque le DNS rebinding.

## Ajouter vos images

1. Déposez vos images PNG ou JPEG dans `corpus/images/`.
2. Dans `corpus/manifest.json`, décrivez au moins cinq paires en suivant `corpus/manifest.example.json`. Chaque paire contient une image `real` et une image `generated`, avec `source`, `credit`, `license` et `evidence`. Les chemins sont relatifs au dossier du manifeste.
3. Préparez les deux images d’une paire aux mêmes dimensions, entre 280 et 1568 pixels par côté, 16 Mio au plus. L’orientation EXIF n’est pas appliquée. Évitez filigranes et différences de qualité qui trahiraient la réponse.
4. Vérifiez puis lancez :

```sh
go run . -check-corpus   # valide le manifeste et les images, sans appel API
go run . -check-jev      # un seul appel réel, potentiellement facturé
go run .
```

Le validateur contrôle les champs de provenance, les dimensions, les formats et les doublons. Il ne certifie pas la véracité de ce que vous déclarez.

Les images sont décodées puis réencodées en JPEG pour retirer les métadonnées. Le joueur et Jev voient exactement les mêmes pixels. Ni le nom du fichier, ni l’origine, ni le crédit ne sont transmis à TypeSafe.

`corpus/images/` et `corpus/manifest.json` sont ignorés par Git, pour éviter de publier par erreur des images sans droits de redistribution.

`scripts/import_sightengine.py` montre comment un corpus local a été importé pour le développement. Il attend des fichiers qui ne sont pas dans le dépôt.

## Déploiement

Le binaire est statique et contient l’interface. Il se compile depuis n’importe quelle machine :

```sh
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o dist/humainvsjev .
```

Sur le serveur, il suffit de copier dans un même dossier le binaire, le `.env` et le corpus (`corpus/manifest.json` et `corpus/images/`), puis de lancer le binaire depuis ce dossier, par exemple avec un service systemd. Il écoute sur `ADDR` (`127.0.0.1:8080` par défaut). Placez devant un reverse proxy qui gère le HTTPS, et réglez `PUBLIC_HOST=jeu.example.com` et `TRUST_PROXY=1`. Par exemple avec Caddy :

```
jeu.example.com {
	encode zstd gzip
	reverse_proxy 127.0.0.1:8080
}
```

Les parties sont en mémoire : redémarrer le service efface les parties en cours. Les statistiques de l’accueil, elles, sont écrites dans `STATS_FILE` à chaque partie terminée : le dossier doit être accessible en écriture au service.

Chaque partie est tracée dans les logs sous un identifiant court (jamais le cookie) : démarrage, résultat de chaque round, puis fin, comptée ou non. Une partie où Jev a manqué un round reste hors des stats de l’accueil ; le log dit sur combien de rounds et pourquoi (`timeout`, `budget`, `quota`…). Une partie quittée en cours est loguée « abandonnée » quand elle est remplacée ou nettoyée après expiration. Au démarrage, le log indique le fichier de stats chargé et son nombre de parties : un compteur revenu à zéro après un redéploiement signale un `STATS_FILE` hors volume persistant.

## Fonctionnement

- `main.go` et `dotenv.go` : configuration, routes HTTP, cookie de session opaque, fichiers statiques embarqués.
- `corpus.go` : lecture, validation et normalisation du manifeste.
- `game.go` : état d’une partie, tirage et mélange des paires, scores, vue publique sans fuite de la réponse.
- `typesafe.go` : client HTTP de l’API TypeSafe et validation stricte des réponses.
- `stats.go` : statistiques cumulées, enregistrées dans un fichier JSON.
- `guard.go` : protections du serveur public (hôtes acceptés, limite par adresse IP, budget quotidien d’appels).
- `health.go` : second essai après une erreur passagère, pause de Jev après des refus répétés.
- `web/` : l’interface.

Pour suivre le code : `POST /api/games` → `Game.start` → deux goroutines, une par image → canal de résultats → `Game.view`. Un `sync.Mutex` protège le choix, les points et le changement de round, et chaque round a un identifiant propre pour qu’une réponse tardive n’affecte pas le suivant.

Jev reçoit la même question pour chaque image, isolément : « cette image est-elle générée par IA ? ». Il désigne l’image qui obtient l’estimation la plus haute. En cas d’égalité exacte, il s’abstient. Si un appel échoue, aucun choix n’est inventé pour Jev, et la partie est signalée comme incomplète.

Gestion des échecs TypeSafe (`typesafe.go`, `health.go`) :

- chaque échec est classé (délai dépassé, réseau, clé refusée, quota, panne serveur, requête rejetée, réponse invalide) et la révélation affiche un message propre à la cause ;
- une erreur passagère (5xx, réseau, 429) est réessayée une fois, si le délai du round laisse au moins une seconde et si le budget du jour le permet. `Retry-After` est respecté jusqu’à 5 secondes ;
- après trois refus d’affilée (clé refusée ou requête rejetée), Jev fait une pause de dix minutes : l’accueil l’annonce et aucune partie ne démarre ;
- un modèle différent de `TYPESAFE_MODEL` dans la réponse est journalisé une fois ;
- les logs ne gardent du corps d’erreur que son code, s’il ressemble à un identifiant (`invalid_api_key`), jamais de texte libre.

Le temps du joueur est mesuré par le serveur, chargement des images et réseau compris. Le temps de Jev est la durée réelle des deux appels lancés en parallèle.

L’accueil affiche des statistiques cumulées, tous joueurs confondus : nombre de parties, victoires, taux de bonnes réponses et temps moyen par réponse, humains contre Jev. Seules les parties terminées où Jev a répondu aux cinq rounds sont comptées. Aucune donnée par joueur n’est conservée. Une partie compte dès la fin de son dernier round. L’accueil relit les stats toutes les 20 secondes tant qu’il est affiché, et dès qu’on revient sur l’onglet. Un fichier de statistiques illisible bloque le démarrage, pour ne jamais écraser l’historique.

Le détail du contrat d’API est dans [docs/typesafe-contract.md](docs/typesafe-contract.md).

## Tests

```sh
go test ./...
go test -race ./...
go vet ./...
```

Les tests couvrent le choix de Jev et les égalités, les scores, le verrouillage des réponses, les délais, les échecs partiels, les réponses tardives et une partie complète par HTTP contre un faux fournisseur. Aucun test n’appelle l’API payante.

`scripts/smoke_live.py` joue une partie réelle contre un serveur lancé localement. Il fait dix appels potentiellement facturés.

## Limites

- L’état des parties est en mémoire : un redémarrage les efface. Sessions de deux heures, 200 au plus.
- Pas d’authentification : sur un serveur public, n’importe qui peut jouer dans la limite de `GAMES_PER_HOUR` et de `DAILY_CALLS`. Une personne qui dispose de nombreuses adresses IP peut épuiser le budget du jour, sans pouvoir le dépasser.
- Le serveur écoute en local par défaut.
