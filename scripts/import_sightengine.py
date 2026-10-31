#!/usr/bin/env python3
"""One-time corpus import. Run: uv run --with pillow scripts/import_sightengine.py"""
import hashlib
import json
from pathlib import Path
from PIL import Image, ImageOps, ImageDraw

root = Path(__file__).resolve().parents[1]
source = root / 'sightengine-images'
metadata = json.loads((source / 'manifest.json').read_text())
out = root / 'corpus' / 'images'
out.mkdir(exist_ok=True)
pairs = {}
thumbs = []
for entry in metadata['images']:
    original = source / entry['file']
    actual = hashlib.sha256(original.read_bytes()).hexdigest()
    if actual != entry['sha256']:
        raise SystemExit(f'Empreinte incorrecte : {entry["file"]}')
    if entry['category'] not in ('ia', 'reelle'):
        raise SystemExit('Catégorie inconnue')
    origin = 'generated' if entry['category'] == 'ia' else 'real'
    pair = entry.get('pair_id', entry.get('pair'))
    if not isinstance(pair, int) or pair < 1:
        raise SystemExit('Identifiant de paire invalide')
    name = f'{pair:02d}-{"b" if origin == "generated" else "a"}.jpg'
    with Image.open(original) as img:
        normalized = ImageOps.fit(ImageOps.exif_transpose(img).convert('RGB'), (900, 600), Image.Resampling.LANCZOS)
        normalized.save(out / name, 'JPEG', quality=90)
        thumb = normalized.resize((270, 180))
        thumbs.append((pair, origin, thumb))
    pairs.setdefault(pair, []).append({
        'path': f'images/{name}',
        'origin': origin,
        'source': entry['url'],
        'credit': 'Sightengine, jeu « Which image is AI? » ; auteur original non précisé',
        'license': 'Utilisée pour une démo non commerciale.',
        'evidence': f'Catégorie selon le catalogue public Sightengine et le recueil fourni : {metadata["source"]}. Original {entry["file"]}, SHA-256 {actual}. Pas de certification indépendante de prise de vue ou de génération.'
    })
if len(pairs) < 5 or any(len(v) != 2 or {x['origin'] for x in v} != {'real', 'generated'} for v in pairs.values()):
    raise SystemExit('Au moins cinq paires complètes réel/IA requises')
manifest = [{'id': f'sightengine-{i:02d}', 'images': sorted(pairs[i], key=lambda x: x['origin'], reverse=True)} for i in sorted(pairs)]
(root / 'corpus' / 'manifest.json').write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + '\n')
sheet = Image.new('RGB', (560, len(pairs)*210), 'white')
positions = {pair: index for index, pair in enumerate(sorted(pairs))}
draw = ImageDraw.Draw(sheet)
for pair, origin, thumb in thumbs:
    x = 0 if origin == 'real' else 285
    y = positions[pair] * 210
    sheet.paste(thumb, (x, y+25))
    draw.text((x+5,y+5), f'Paire {pair} / {origin}', fill='black')
sheet.save('/tmp/humainvsjev-corpus.jpg')
print(f'Import terminé : {len(pairs)} paires, {len(thumbs)} JPEG 900 × 600, empreintes originales vérifiées.')
