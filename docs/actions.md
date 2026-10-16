# Actions: calling your backend

When a caller reaches a screen with an `action`, the runtime POSTs the session to your URL. It waits up to `timeout_ms` (2 seconds by default), then shows the screen using whatever variables you returned.

## What your backend receives

```http
POST /ussd/student HTTP/1.1
Content-Type: application/json
X-USSD-Timestamp: 1792124133
X-USSD-Signature: v1=5d1c0e...

{
  "session_id": "ATUid_9a2f...",
  "gateway": "africastalking",
  "phone": "233241234567",
  "network": "62001",
  "screen": "pay_lookup",
  "vars": { "phone": "233241234567", "network": "62001", "student_id": "1001" }
}
```

## What to send back

```json
{ "vars": { "student_name": "Ama Mensah", "class_name": "JHS 2", "balance": "1,250.00" } }
```

To send the caller somewhere else, return a screen from the action's `can_go_to` list:

```json
{ "next": "not_found" }
```

**Rules:**
- Reply `2xx` with JSON, at most 64 KB.
- Variable names are lowercase letters, digits and `_`. Values are at most 160 characters. A session holds at most 30 variables.
- You can't overwrite `phone` or `network`.
- A `next` outside `can_go_to` is refused, so a compromised or buggy backend can't route callers anywhere.

The runtime ends the session with the action's `error_message` if any of these happen:
- your backend returns a non-2xx status;
- the reply is invalid or breaks a rule above;
- it is slower than `timeout_ms`.

Answer quickly. The caller is waiting on a network that will drop the session.

## Verifying the signature

`X-USSD-Signature` is `v1=` + hex HMAC-SHA256 of `"<timestamp>.<raw body>"`, keyed with `ACTION_SECRET`. This is the same scheme [Webhook Relay](https://github.com/Jacksonoseikojo1122/webhook-relay) uses. Reject requests older than 5 minutes.

### Node.js (Express)

```ts
import crypto from "node:crypto";
import express from "express";

const app = express();
app.post("/ussd/student", express.raw({ type: "application/json" }), (req, res) => {
  const ts = req.get("x-ussd-timestamp") ?? "";
  const sig = req.get("x-ussd-signature") ?? "";
  const want = "v1=" + crypto.createHmac("sha256", process.env.ACTION_SECRET!).update(`${ts}.${req.body}`).digest("hex");
  const fresh = Math.abs(Date.now() / 1000 - Number(ts)) < 300;
  if (!fresh || sig.length !== want.length || !crypto.timingSafeEqual(Buffer.from(sig), Buffer.from(want))) {
    return res.status(401).end();
  }
  const { vars } = JSON.parse(req.body.toString());
  const student = findStudent(vars.student_id);
  if (!student) return res.json({ next: "not_found" });
  res.json({ vars: { student_name: student.name, class_name: student.className, balance: student.balance } });
});
```

### Python (FastAPI)

```python
import hashlib, hmac, os, time
from fastapi import FastAPI, Header, HTTPException, Request

SECRET = os.environ["ACTION_SECRET"].encode()
app = FastAPI()

@app.post("/ussd/student")
async def student(request: Request, x_ussd_timestamp: str = Header(""), x_ussd_signature: str = Header("")):
    raw = await request.body()
    want = "v1=" + hmac.new(SECRET, f"{x_ussd_timestamp}.".encode() + raw, hashlib.sha256).hexdigest()
    if not x_ussd_timestamp.isdigit() or abs(time.time() - int(x_ussd_timestamp)) > 300 or not hmac.compare_digest(want, x_ussd_signature):
        raise HTTPException(401)
    vars = (await request.json())["vars"]
    s = find_student(vars["student_id"])
    return {"next": "not_found"} if s is None else {"vars": {"student_name": s.name, "class_name": s.class_name, "balance": s.balance}}
```

### PHP

```php
$raw = file_get_contents('php://input');
$ts  = $_SERVER['HTTP_X_USSD_TIMESTAMP'] ?? '';
$want = 'v1=' . hash_hmac('sha256', $ts . '.' . $raw, getenv('ACTION_SECRET'));
if (abs(time() - (int) $ts) > 300 || !hash_equals($want, $_SERVER['HTTP_X_USSD_SIGNATURE'] ?? '')) {
    http_response_code(401); exit;
}
$vars = json_decode($raw, true)['vars'];
header('Content-Type: application/json');
echo json_encode(['vars' => ['student_name' => 'Ama Mensah', 'balance' => '1,250.00']]);
```

### Go

See [`examples/demo-backend/main.go`](../examples/demo-backend/main.go).

## Payments

The school-fees example ends with a "payment request". A real backend would start a mobile money collection there through a licensed provider, and the caller approves it on their own phone.

**Never ask for a mobile money PIN over USSD.** Session data passes through the gateway and the network.
