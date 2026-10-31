#!/usr/bin/env python3
"""Manual live check: 5 rounds / 10 potentially billable TypeSafe calls.
Run against a locally running server: python3 scripts/smoke_live.py
This is a transport/state smoke test, never a blind detection benchmark.
"""
import argparse
import http.cookiejar
import json
import time
import urllib.error
import urllib.request

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--base-url', default='http://127.0.0.1:8080')
args = parser.parse_args()
client = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))

def request(path, body=None):
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(args.base_url + path, data=data, headers={'Content-Type': 'application/json'})
    with client.open(req, timeout=15) as response:
        return json.load(response)

config = request('/api/config')
if not config['live']:
    raise SystemExit('Mode réel non configuré : vérifier clé et corpus.')
view = request('/api/games', {'mode': 'live'})
seen = set()
scores = [0, 0]
unavailable = 0
for number in range(1, 6):
    assert view['number'] == number and view['roundId'] not in seen
    seen.add(view['roundId'])
    assert 'reveal' not in view
    for path in view['images']:
        assert path.startswith('/images/') and not path.endswith('.jpg')
        with client.open(args.base_url + path, timeout=15) as response:
            assert response.headers.get_content_type() == 'image/jpeg'
            assert response.read(2) == b'\xff\xd8'
    round_id = view['roundId']
    view = request('/api/choose', {'roundId': round_id, 'choice': 0})
    try:
        request('/api/choose', {'roundId': round_id, 'choice': 1})
        raise AssertionError('Double choix accepté')
    except urllib.error.HTTPError as error:
        assert error.code == 409
    deadline = time.monotonic() + 65
    while not view.get('reveal'):
        if time.monotonic() > deadline:
            raise SystemExit('Révélation bloquée')
        time.sleep(.15)
        view = request('/api/round')
    result = view['reveal']
    assert result['player'] == 0
    assert result['humanPoints'] == int(result['truth'] == 0)
    if result['unavailable']:
        unavailable += 1
        assert result['jevPoints'] == 0 and 'values' not in result
    else:
        a, b = result['values']
        expected = -1 if a == b else (0 if a > b else 1)
        assert result['jev'] == expected
        assert result['jevPoints'] == int(expected == result['truth'])
        assert all(result['models'])
        assert result['totalMs'] >= max(result['callMs'])
    scores[0] += result['humanPoints']
    scores[1] += result['jevPoints']
    assert scores == [view['humanScore'], view['jevScore']]
    print(f'Round {number}/5 validé ; modèle(s) : {result["models"]} ; analyses : {result["totalMs"]} ms ; indisponible : {result["unavailable"]}')
    if number < 5:
        view = request('/api/next', {'roundId': round_id})
try:
    request('/api/next', {'roundId': view['roundId']})
    raise AssertionError('Sixième round accepté')
except urllib.error.HTTPError as error:
    assert error.code == 409
print(f'Parcours réel terminé : 5 rounds, {unavailable} indisponible(s). Aucun score de reconnaissance revendiqué.')
if unavailable:
    raise SystemExit(1)
