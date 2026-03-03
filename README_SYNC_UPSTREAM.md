# Sync Upstream (guida rapida)

Questa guida serve per sincronizzare il tuo fork (`origin`) con il repository originale (`upstream`) senza perdere commit locali.

## Remoti: regola base

- `origin` = tuo fork GitHub
- `upstream` = repository originale (antoniomika/sish)

## Setup remoto (una sola volta)

```bash
# dentro al repository locale
git remote -v
git remote add upstream https://github.com/antoniomika/sish.git
git fetch upstream --prune
```

Verifica:

```bash
git remote -v
```

---

## I 2 comandi standard di sync

Da eseguire quando sei sul branch da aggiornare (es. `main`):

```bash
git fetch upstream --prune
git rebase upstream/main
```

---

## Flusso completo e sicuro (consigliato)

```bash
# 0) branch corretto e working tree pulito
git checkout main
git status

# 1) backup locale rapido
git branch backup/main-$(date +%Y%m%d-%H%M)

# 2) aggiorna riferimenti remoti
git fetch upstream --prune
git fetch origin --prune

# 3) riallinea il branch locale a upstream
git rebase upstream/main

# 4) pubblica su origin (fork)
git push origin main --force-with-lease
```

Se non vuoi riscrivere la storia, usa merge al posto di rebase:

```bash
git fetch upstream --prune
git merge --ff-only upstream/main
git push origin main
```

---

## Gestione conflitti

Quando il rebase si ferma:

```bash
git status
# risolvi i file in conflitto
git add <file-risolti>
git rebase --continue
```

Alternative utili:

```bash
git rebase --skip    # salta commit corrente (solo se ha senso)
git rebase --abort   # annulla tutto il rebase
```

---

## Rollback rapido

Tornare allo stato pre-rebase:

```bash
git reflog
git reset --hard HEAD@{1}
```

Tornare al backup creato:

```bash
git checkout main
git reset --hard backup/main-YYYYMMDD-HHMM
```

---

## Troubleshooting

- `fatal: No such remote 'upstream'` → esegui `git remote add upstream ...`
- `non-fast-forward` in push → fai `fetch` + `rebase` e riprova
- push rifiutato con `--force-with-lease` → qualcuno ha pushato nel frattempo: rifai `fetch` e rebase
- working tree sporco → fai commit o `git stash -u` prima del sync
- branch sbagliato → `git branch --show-current` e torna su `main`

---

## Checklist

- [ ] `upstream` configurato correttamente
- [ ] branch corretto (`main` o quello target)
- [ ] working tree pulito (`git status`)
- [ ] backup branch creato
- [ ] `git fetch upstream --prune` eseguito
- [ ] `git rebase upstream/main` (o `merge`) completato
- [ ] push su `origin` completato (`--force-with-lease` solo se rebase)

## Goal
Sincronizzare in sicurezza `main` del fork con `upstream/main`, mantenendo controllo su conflitti e rollback.

## One-time setup
1. Verificare remoti correnti (`git remote -v`).
2. Aggiungere `upstream` puntando al repository originale.
3. Verificare che:
   - `origin` = fork personale
   - `upstream` = repo originale

## Standard sync (2 commands)
1. `git fetch upstream --prune`
2. `git rebase upstream/main`

## Safe full flow
1. Checkout su `main` e controllo working tree pulito (`git status`).
2. Creazione backup branch timestampato.
3. Fetch da `upstream` e `origin`.
4. Rebase di `main` locale su `upstream/main`.
5. Push verso `origin/main` con `--force-with-lease`.

## Alternative without history rewrite
1. `git fetch upstream --prune`
2. `git merge --ff-only upstream/main`
3. `git push origin main`

## Conflict handling
1. `git status` per identificare i file in conflitto.
2. Risoluzione manuale + `git add`.
3. `git rebase --continue`.
4. Se necessario: `git rebase --skip` o `git rebase --abort`.

## Rollback plan
1. Usare `git reflog` per trovare stato precedente.
2. `git reset --hard HEAD@{1}` per rollback rapido.
3. In alternativa reset al branch backup.

## Troubleshooting checklist
- `upstream` non configurato (`No such remote`) -> aggiungere/ correggere remote.
- Push rifiutato (`non-fast-forward`) -> rifare fetch + rebase.
- Working tree sporco -> commit o stash prima del sync.
- Branch errato -> verificare con `git branch --show-current`.

## Done criteria
- `main` locale allineato a `upstream/main`.
- `origin/main` aggiornato.
- Nessun conflitto pendente o rebase aperto.
