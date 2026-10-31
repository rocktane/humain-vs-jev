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

La clé n’est jamais envoyée au navigateur. Ne placez aucun secret dans `web/` : ce dossier est incorporé au binaire.

Chaque round déclenche deux appels à TypeSafe, soit dix par partie complète. Ces appels peuvent être facturés.

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

Sur le serveur, il suffit de copier dans un même dossier le binaire, le `.env` et le corpus (`corpus/manifest.json` et `corpus/images/`), puis de lancer le binaire depuis ce dossier, par exemple avec un service systemd. Il écoute sur `ADDR` (`127.0.0.1:8080` par défaut). Placez devant un reverse proxy qui gère le HTTPS, par exemple Caddy :

```
jeu.example.com {
	encode zstd gzip
	reverse_proxy 127.0.0.1:8080
}
```

Les parties sont en mémoire : redémarrer le service efface les parties en cours.

## Fonctionnement

- `main.go` et `dotenv.go` : configuration, routes HTTP, cookie de session opaque, fichiers statiques embarqués.
- `corpus.go` : lecture, validation et normalisation du manifeste.
- `game.go` : état d’une partie, tirage et mélange des paires, scores, vue publique sans fuite de la réponse.
- `typesafe.go` : client HTTP de l’API TypeSafe et validation stricte des réponses.
- `web/` : l’interface.

Pour suivre le code : `POST /api/games` → `Game.start` → deux goroutines, une par image → canal de résultats → `Game.view`. Un `sync.Mutex` protège le choix, les points et le changement de round, et chaque round a un identifiant propre pour qu’une réponse tardive n’affecte pas le suivant.

Jev reçoit la même question pour chaque image, isolément : « cette image est-elle générée par IA ? ». Il désigne l’image qui obtient l’estimation la plus haute. En cas d’égalité exacte, il s’abstient. Si un appel échoue, aucun choix n’est inventé pour Jev, et la partie est signalée comme incomplète.

Le temps du joueur est mesuré par le serveur, chargement des images et réseau compris. Le temps de Jev est la durée réelle des deux appels lancés en parallèle.

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
- Pas de quota d’appels par visiteur ni d’authentification. Sur un serveur public, n’importe qui peut lancer des parties facturées : ajoutez un mot de passe ou une limite de débit au reverse proxy si besoin.
- Le serveur écoute en local par défaut.
