# Flow format

A flow is JSON. Anything [USSD Flow Studio](https://github.com/Jacksonoseikojo1122/nextjs-ussd-flow-studio) exports runs unchanged and answers exactly as the Studio's simulator does. Flow Studio's own engine tests run against this runtime in CI ([`conformance_test.go`](../internal/server/conformance_test.go)).

```json
{
  "start": "main",
  "nodes": {
    "main": {
      "prompt": "Welcome to Demo Wallet",
      "kind": "menu",
      "options": [
        { "key": "1", "label": "Check balance", "next": "balance" },
        { "key": "2", "label": "Exit", "next": "bye" }
      ]
    },
    "balance": { "prompt": "Your balance is GHS 120.50.", "kind": "end" },
    "bye": { "prompt": "Thank you.", "kind": "end" }
  }
}
```

Screen ids start with a letter and use letters, digits, `_` or `-` (up to 40 characters). Routing is always by id, never by prompt text.

## Screen kinds

| Kind | Shows | Session |
| --- | --- | --- |
| `menu` | the prompt, then `key. label` for each option | continues (CON) |
| `end` | the prompt | ends (END) |
| `input` | the prompt; the caller types an answer | continues (CON) |

`menu` and `end` come from Flow Studio. `input` is new.

## Input screens

```json
"ask_amount": {
  "prompt": "Enter amount in GHS",
  "kind": "input",
  "input": { "name": "amount", "type": "amount", "min": "1", "max": "5000", "next": "confirm" }
}
```

| Field | Meaning |
| --- | --- |
| `name` | Session variable the answer is stored in: lowercase letters, digits, `_`. `phone` and `network` are reserved |
| `type` | `text`, `digits`, `amount` or `phone_gh` |
| `next` | Screen after a valid answer |
| `min_length`, `max_length` | For `text` and `digits`. `text` defaults to a 60-character maximum |
| `min`, `max` | For `amount`, e.g. `"1"` or `"10.50"` |
| `error` | Shown above the prompt when an answer is rejected. Each type has a sensible default |

What each type accepts and stores:

| Type | Accepts | Stores |
| --- | --- | --- |
| `text` | anything, trimmed | as typed |
| `digits` | `0-9` only (leading zeros kept) | as typed |
| `amount` | `10`, `10.5`, `10.50` | `10.50` (always two decimals) |
| `phone_gh` | `024 123 4567`, `0241234567`, `241234567`, `+233 24 123 4567` | `233241234567` |

A rejected answer asks the question again. After three wrong answers in a row, the session ends.

## Variables in prompts

Prompts and option labels can use `{{name}}`. Variables come from input screens, from your backend's action replies, and two built-ins:
- `{{phone}}`: the caller's number, digits only, e.g. `233241234567`.
- `{{network}}`: the network as the gateway reported it.

A variable that hasn't been set shows as nothing.

## Actions

Any screen can call your backend when the caller arrives on it. The reply can set variables, or send the caller to another screen:

```json
"pay_lookup": {
  "prompt": "Pay for {{student_name}}?\nOutstanding: GHS {{balance}}",
  "kind": "menu",
  "action": { "url": "$BACKEND_URL/ussd/student", "timeout_ms": 2000, "can_go_to": ["not_found"] },
  "options": [ ... ]
}
```

| Field | Meaning |
| --- | --- |
| `url` | `$NAME` parts are read from the environment |
| `timeout_ms` | 100 to 4000; default 2000. Networks drop slow sessions, so keep it short |
| `can_go_to` | The only screens your backend may send the caller to |
| `error_message` | Shown (and the session ended) if the call fails, times out or returns something not allowed |

See [actions.md](actions.md) for the request your backend receives and how to verify it.

## Checks when the flow loads

**Errors** (the runtime won't start):
- no start screen;
- a menu with no options;
- a non-digit, duplicate or unlinked key;
- a link to a missing screen;
- an input screen without valid `input` settings;
- an action with a bad URL, or a timeout outside 100–4000 ms.

**Warnings:**
- an empty prompt;
- an unreachable screen;
- options on an end screen;
- a screen over 182 characters, which many networks cut;
- a screen over 120 characters, which is Nalo's limit;
- plain `http` to a remote backend.

## Things to know

- **Africa's Talking without stored sessions.** Africa's Talking sends every key pressed so far (`1*2*0`). If a flow has no input screens and no actions, the runtime can rebuild the session from those keys alone, just like Flow Studio's exported servers. Flows with inputs or actions need their stored session, because replaying could call your backend twice. If that session has expired, the caller is told to dial again.
- **Avoid special characters.** Africa's Talking asks for menus without them, because some networks can't display them.
