# Forwarders Dedicated Logs

Questo documento descrive la funzionalita` di logging dedicato per forwarder, utile in produzione per separare i log per `Connection ID`.

## Obiettivo

Raccogliere log separati per forwarder in file distinti, con naming stabile e configurazione di retention/rotation/compression.

La feature registra:
- richieste HTTP/HTTPS inoltrate ai servizi forwardati
- eventi di avvio forward HTTP/TCP/TCP Alias
- connessioni runtime accettate su TCP/TCP Alias
- eventi di rifiuto forward per policy `allowed-forwarder`

## Attivazione

La funzionalita` e` governata da:

- `--forwarders-log=enable|disable`

Valore consigliato in produzione:

```bash
--forwarders-log=enable
```

## Directory di output

Directory dove vengono creati i file per-forwarder:

- `--forwarders-log-dir=/fwlogs`

Esempio:

```bash
--forwarders-log-dir=/var/log/sish/fwlogs
```

## Naming file

Pattern base:
- HTTP/HTTPS: `id-domain`
- TCP: `id-port`
- TCP Alias: `id-alias_port`

Esempi:
- `pippoid-mysubdomain.example.com`
- `pluto-id-9000`
- `paperinoalias-myalias_9000`

Note:
- i nomi vengono sanitizzati automaticamente (caratteri non validi convertiti)
- non viene aggiunta estensione obbligatoria

## Parametri forwarders-log-*

La feature supporta parametri dedicati di tipo production-grade:

- `--forwarders-log`
  - `enable` oppure `disable`
- `--forwarders-log-dir`
  - directory base dei file per-forwarder
- `--forwarders-log-time-format`
  - formato timestamp per riga log
  - se vuoto, usa `--time-format`
- `--forwarders-log-max-size`
  - dimensione massima (MB) prima della rotazione
- `--forwarders-log-max-backups`
  - numero massimo di file ruotati mantenuti
- `--forwarders-log-max-age`
  - giorni massimi di retention dei file ruotati
- `--forwarders-log-compress`
  - compressione dei file ruotati

## Default

Default presenti in configurazione:

```yaml
forwarders-log: disable
forwarders-log-dir: /fwlogs
forwarders-log-time-format: ""
forwarders-log-max-size: 100
forwarders-log-max-backups: 10
forwarders-log-max-age: 30
forwarders-log-compress: false
```

## Esempio avvio completo

```bash
./sish \
  --forwarders-log=enable \
  --forwarders-log-dir=/var/log/sish/fwlogs \
  --forwarders-log-max-size=200 \
  --forwarders-log-max-backups=20 \
  --forwarders-log-max-age=60 \
  --forwarders-log-compress=true
```

## Integrazione con logging globale

I log per-forwarder non sostituiscono i log globali (`--log-to-stdout`, `--log-to-file`):
- logging globale: vista applicativa complessiva
- logging forwarders: vista per singolo tunnel/ID

Usarli insieme e` consigliato in produzione.

## Considerazioni operative

1. Storage:
- con molti forwarder attivi, pianificare spazio disco adeguato in `forwarders-log-dir`.

2. Security:
- proteggere la directory log con permessi appropriati e backup policy.

3. Performance:
- la scrittura e` concorrente-safe e con writer dedicato per file.
- attivare compressione se retention alta.

4. Correlazione incident:
- combinare `Connection ID` + file forwarder per audit puntuale.

## File coinvolti nello sviluppo

- `cmd/sish.go` (nuovi flag CLI)
- `config.example.yml` (nuove chiavi config)
- `utils/forwarder_logs.go` (manager log forwarder, naming, rotation)
- `httpmuxer/httpmuxer.go` (log richieste HTTP/HTTPS per forwarder)
- `sshmuxer/httphandler.go` (eventi start HTTP/HTTPS forward)
- `sshmuxer/tcphandler.go` (eventi start TCP forward)
- `sshmuxer/aliashandler.go` (eventi start TCP Alias forward)
- `utils/state.go` (eventi runtime connessioni TCP)
- `sshmuxer/channels.go` (eventi runtime connessioni TCP Alias)
- `sshmuxer/requests.go` (rifiuti policy `allowed-forwarder`)
