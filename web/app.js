'use strict';
const $ = id => document.getElementById(id);
let view, poll, epoch = 0, busy = false, loaded = [false, false], selectedChoice = null;
const seconds = ms => `${(ms / 1000).toLocaleString('fr-FR', {minimumFractionDigits: 2, maximumFractionDigits: 2})} s`;
function showError(error) { $('error').textContent = error.message; $('error').hidden = false; }
async function api(path, body) {
 const controller = new AbortController();
 const timer = setTimeout(() => controller.abort(), 12000);
 try {
  const res = await fetch(`/api/${path}`, {signal: controller.signal, ...(body === undefined ? {} : {method:'POST', headers:{'Content-Type':'application/json'}, body:JSON.stringify(body)})});
  const data = await res.json();
  if (!res.ok) { const error = new Error(data.error || 'Le serveur ne répond pas.'); error.status = res.status; throw error; }
  return data;
 } catch (err) {
  if (err.name === 'AbortError') throw new Error('Le serveur met trop de temps à répondre. Réessaie.');
  throw err;
 } finally { clearTimeout(timer); }
}

function screen(id) { document.body.classList.toggle('in-game', id === 'game'); for (const s of ['home','game','finish']) $(s).hidden = s !== id; }
function enableChoices() { document.querySelectorAll('[data-choice]').forEach(b => b.disabled = busy || view.chosen || !loaded.every(Boolean)); }
function render(v) {
 const fresh = !view || view.roundId !== v.roundId;
 view = v;
 $('progress').textContent = `Round ${v.number} / 5`;
 $('human-score').textContent = v.humanScore; $('jev-score').textContent = v.jevScore;
 $('jev-status').textContent = v.ready ? 'Jev a choisi' : 'Jev cherche…';
 if (fresh) {
  loaded = [false,false]; selectedChoice = null;
  for (let i=0;i<2;i++) { const img = $(`image-${i}`); img.onload = () => {if (view.roundId !== v.roundId) return; loaded[i] = true; enableChoices();}; img.onerror = () => showError(new Error('Image indisponible. Relance la partie.')); img.src = v.images[i]; }
 }
 for (let i=0;i<2;i++) {
  const picked = v.reveal ? v.reveal.player === i : selectedChoice === i;
  const b = document.querySelector(`[data-choice="${i}"]`);
  $(`card-${i}`).classList.toggle('selected', picked);
  $(`card-${i}`).classList.toggle('correct', !!v.reveal && v.reveal.truth === i);
  b.setAttribute('aria-pressed', String(picked));
  b.textContent = picked ? 'Ton choix' : 'C’est l’image IA';
  $(`tag-${i}`).hidden = !v.reveal;
  if (v.reveal) $(`tag-${i}`).textContent = v.reveal.truth === i ? 'Générée par IA' : 'Photo réelle';
 }
 enableChoices();
 $('reveal').classList.toggle('shown', !!v.reveal);
 if (v.reveal) {
  const r = v.reveal;
  $('jev-status').textContent = r.unavailable ? 'Jev indisponible' : 'Jev a choisi';
  $('reveal-title').textContent = `${r.humanPoints ? 'Bien vu' : 'Raté'}, l’image IA était la ${r.truth === 0 ? 'A' : 'B'}.`;
  const jev = r.unavailable ? 'Jev n’a pas répondu à temps.' : `Jev ${r.jevPoints ? 'a vu juste' : 's’est trompé'} en ${seconds(r.totalMs)}.`;
  $('reveal-detail').textContent = `${jev} Toi, tu as mis ${seconds(r.playerMs)}.`;
  $('next').textContent = v.number === 5 ? 'Voir le résultat final' : 'Round suivant';
  if (poll) {clearTimeout(poll); poll = null;}
 }
}
function schedulePoll(e) {
 clearTimeout(poll);
 if (e !== epoch || !view || view.reveal) return;
 poll = setTimeout(async () => {
  try { const v = await api('round'); if (e !== epoch) return; render(v); schedulePoll(e); }
  catch (err) { if(e === epoch) {showError(err); if(err.status === 401) { leave(); showError(err); } else if(view && !view.reveal) schedulePoll(e);} }
 }, 450);
}
async function start() {
 if (busy) return; busy=true; epoch++; clearTimeout(poll); const e=epoch;
 $('error').hidden=true; $('start').disabled=true;
 try {const v=await api('games',{}); view=null; screen('game'); render(v); $('round-title').tabIndex=-1; $('round-title').focus(); schedulePoll(e);}
 catch(err){showError(err);} finally {busy=false; if(view) enableChoices(); await config();}
}
$('start').onclick = start;
document.querySelectorAll('[data-choice]').forEach(b => b.onclick = async () => {
 if (busy || view.chosen || !loaded.every(Boolean)) return;
 busy=true; clearTimeout(poll); const e=++epoch; const id=view.roundId;
 selectedChoice = Number(b.dataset.choice); render(view); enableChoices();
 try {const v=await api('choose',{roundId:id,choice:selectedChoice});if(e===epoch)render(v);}
 catch(err){showError(err);try{if(e===epoch)render(await api('round'));}catch{}}
 finally {busy=false;enableChoices();schedulePoll(e);}
});
$('next').onclick = async () => {
 if(busy || !view.reveal) return;
 if(view.number === 5) {finish();return;}
 busy=true; enableChoices(); $('next').disabled=true; const e=++epoch; clearTimeout(poll); $('error').hidden=true;
 try {const v=await api('next',{roundId:view.roundId});render(v);$('round-title').focus();schedulePoll(e);}catch(err){showError(err);try{render(await api('round'));schedulePoll(e);}catch{}}finally{busy=false;$('next').disabled=false;enableChoices();}
};
function finish() {
 screen('finish'); const diff=view.humanScore-view.jevScore;
 $('final-title').textContent = view.unavailableRounds > 0 ? 'Partie terminée, duel incomplet.' : diff>0 ? 'Le regard humain l’emporte.' : diff<0 ? 'Jev remporte ce duel.' : 'Deux regards, même score.';
 $('final-human').textContent = `${view.humanScore} / 5`;
 $('final-jev').textContent = `${view.jevScore} / 5`;
 $('final-message').textContent = view.unavailableRounds > 0 ? `Jev n’a pas répondu sur ${view.unavailableRounds} round(s).` : '';
 $('finish').focus();
}
$('replay').onclick = start;
function leave() { epoch++; clearTimeout(poll); screen('home'); config(); $('error').hidden=true; $('start').focus(); if ($('teaser-1').complete) animateLens($('teaser-1')); }
async function config() {
 try {
  const c=await api('config'); $('start').disabled=!c.live;
  $('availability').hidden = c.live;
  $('availability').textContent = c.budgetExhausted && c.hasKey && c.pairs >= 5
   ? 'Jev a atteint son quota d’analyses pour aujourd’hui. Reviens demain.'
   : 'Jev indisponible : ' + [c.hasKey ? '' : 'clé TypeSafe absente.', c.pairs<5 ? 'Il faut au moins 5 paires d’images.' : ''].filter(Boolean).join(' ');
  renderStats(c.stats);
 } catch(err){showError(err);}
}
// Stats cumulées des parties complètes, tous joueurs confondus.
// Le meilleur chiffre de chaque ligne reçoit une pastille ; aucune en cas d'égalité.
function renderStats(s) {
 $('stats').hidden = !s || !s.games;
 if ($('stats').hidden) return;
 const n = v => v.toLocaleString('fr-FR');
 const pct = v => Math.round(100 * v / s.rounds);
 const avg = ms => ms / s.rounds;
 $('stat-games').textContent = n(s.games);
 const rows = [
  ['wins', s.humanWins, s.jevWins, n, 1],
  ['correct', pct(s.humanCorrect), pct(s.jevCorrect), v => `${v} %`, 1],
  ['time', avg(s.humanMs), avg(s.jevMs), v => `${(v / 1000).toLocaleString('fr-FR', {minimumFractionDigits: 1, maximumFractionDigits: 1})} s`, -1],
 ];
 for (const [key, human, jev, format, better] of rows) {
  // Compare les valeurs affichées, pour ne pas souligner un écart invisible.
  const shown = [format(human), format(jev)], lead = (human - jev) * better;
  ['human', 'jev'].forEach((side, i) => {
   const cell = $(`stat-${side}-${key}`), span = document.createElement('span');
   span.textContent = shown[i];
   cell.replaceChildren(span);
   cell.classList.toggle('best', shown[0] !== shown[1] && (i === 0 ? lead > 0 : lead < 0));
  });
 }
}
config();
// Quasi temps réel sans WebSocket : l'accueil relit les stats toutes les 20 s tant qu'il est
// visible, et tout de suite au retour sur l'onglet. Une erreur réseau ici reste silencieuse.
async function refreshStats() {
 if ($('home').hidden || document.hidden) return;
 try { renderStats((await api('config')).stats); } catch {}
}
setInterval(refreshStats, 20000);
document.addEventListener('visibilitychange', refreshStats);
// Accueil : deux images du corpus, sans leur origine. La loupe se promène sur la seconde
// et montre, agrandi, le point exact qu'elle survole.
const ZOOM = 2.6;
const PATH = [[.30,.38],[.68,.30],[.74,.66],[.42,.72],[.24,.52]];
function placeLens(img, x, y) {
 const lens = $('lens'), view = $('lens-view');
 const w = img.offsetWidth, h = img.offsetHeight, size = lens.offsetWidth, d = view.offsetWidth;
 const px = x * w, py = y * h;
 lens.style.transform = `translate(${px - size / 2}px, ${py - size / 2}px)`;
 view.style.backgroundSize = `${w * ZOOM}px ${h * ZOOM}px`;
 view.style.backgroundPosition = `${d / 2 - px * ZOOM}px ${d / 2 - py * ZOOM}px`;
}
function animateLens(img) {
 const still = matchMedia('(prefers-reduced-motion: reduce)').matches;
 const leg = 3200, ease = t => t < .5 ? 4*t*t*t : 1 - Math.pow(-2*t + 2, 3) / 2;
 const frame = now => {
  const k = now / leg, i = Math.floor(k) % PATH.length, t = ease(k % 1);
  const [a, b] = [PATH[i], PATH[(i + 1) % PATH.length]];
  placeLens(img, a[0] + (b[0] - a[0]) * t, a[1] + (b[1] - a[1]) * t);
  if (!$('home').hidden && !still) requestAnimationFrame(frame);
 };
 if (still) placeLens(img, PATH[0][0], PATH[0][1]); else requestAnimationFrame(frame);
}
function teaser() {
 for (let i=0;i<2;i++) {
  const img=$(`teaser-${i}`);
  img.onerror=()=>document.querySelector('.showcase').classList.add('empty');
  img.onload=()=>{ if (i===1) { $('lens-view').style.backgroundImage=`url(${img.src})`; animateLens(img); } };
  img.src=`/teaser/${i}`;
 }
}
teaser();
