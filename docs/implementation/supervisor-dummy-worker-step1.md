# Step 1 Implementation: Supervisor + Dummy Worker

## Scope
This step implements a testable runtime with:
1. A complete `autonous-supervisor` process lifecycle loop.
2. A `autonous-worker` dummy Telegram loop.
3. Dummy behavior: echo worker instance id + input text.
4. Intentional self-termination every 2 handled messages.

## Expected Runtime Behavior
1. Supervisor starts worker instance `W000001`.
2. Telegram message arrives.
3. Worker replies with format:
- `[DummyWorker:W000001 #1] <your message>`
4. After two handled messages by the same worker instance, worker exits with code `17`.
5. Supervisor detects exit and starts next worker instance (for example `W000002`).
6. New Telegram messages are replied by the new worker id.

## Implementation Steps
1. Update worker binary `src/bin/autonous-worker.rs`:
- Keep Telegram polling and `sendMessage`.
- Keep SQLite (`history`, `kv`) and Telegram offset persistence (`kv.telegram_offset`).
- Read `WORKER_INSTANCE_ID` from env.
- Read `WORKER_SUICIDE_EVERY` from env (default `2`).
- Reply with current worker id and local handled count.
- Exit intentionally after every N handled messages.

2. Update supervisor binary `src/bin/autonous-supervisor.rs`:
- Keep process supervision loop.
- Maintain sequential worker instance id in SQLite key `worker_instance_seq`.
- Before each launch, generate id like `W000001` and pass via env `WORKER_INSTANCE_ID`.
- Restart worker after exit.
- Keep crash-loop tracking and rollback hooks.

3. Keep startup simple in `startup.sh`:
- Build binaries if missing.
- Exec supervisor only.

## Build and Run Steps
1. Compile check:
```bash
cargo check --bins
```

2. Build image:
```bash
docker build -t autonous-agent:dev .
```

3. Recreate container:
```bash
docker rm -f autonous-agent || true
docker run -d \
  --name autonous-agent \
  --restart unless-stopped \
  --env-file ~/.env \
  -e WORKER_SUICIDE_EVERY=2 \
  autonous-agent:dev
```

4. Verify logs:
```bash
docker logs -f --tail 80 autonous-agent
```

You should see lines like:
- `[supervisor] starting worker instance W000001`
- `dummy worker running id=W000001 suicide_every=2`
- After two messages:
- `dummy worker id=W000001 handled 2 messages; exiting intentionally`
- `[supervisor] starting worker instance W000002`

## Telegram Test Procedure
1. Send message A to bot.
- Expect reply from `W000001` with `#1`.

2. Send message B to bot.
- Expect reply from `W000001` with `#2`.
- Worker should then exit.

3. Send message C to bot.
- Expect reply from `W000002` with `#1`.

If step 3 succeeds, supervisor restart path is verified.

## Notes
1. This step is intentionally dummy-only for lifecycle verification.
2. Step 2 will replace dummy logic with real model API task execution.
