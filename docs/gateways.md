# Gateways

Each gateway posts to its own endpoint. The runtime turns every format into the same steps and answers in that gateway's format. Each format below comes from the provider's own documentation; those pages are linked under each gateway.

Set `GATEWAY_TOKEN` and add `?token=...` to the callback URL you give the gateway. Then only requests that know the token are answered.

## Africa's Talking: `POST /ussd/africastalking`

**Request** (form-encoded):

| Field | Meaning |
| --- | --- |
| `sessionId` | Same for the whole session |
| `phoneNumber` | e.g. `+233241234567` |
| `networkCode` | The caller's network |
| `serviceCode` | The USSD code dialled |
| `text` | `""` on the first request, then every input so far joined with `*`, e.g. `1*1001*250` |

**Reply:** `text/plain`, starting with `CON ` (wait for input) or `END ` (close).

**Notes:**
- The runtime works out the new input by removing the part of `text` it has already handled.
- Free-text answers can contain `*` and still arrive intact.
- A repeated request gets the same screen again.
- Source: [Africa's Talking, Handling a USSD session](https://developers.africastalking.com/docs/ussd/handle_sessions).

## Hubtel: `POST /ussd/hubtel`

**Request** (JSON):
- `SessionId`, `Mobile`, `ServiceCode`, `Operator`, `Sequence`, `ClientState`, `Platform`.
- `Type`: `Initiation` for a new session, `Response` for input, `Release` or `Timeout` when it's over.
- `Message`: the latest input.

**Reply** (JSON):
- `SessionId` and `Message`.
- `Type`: `Response` to continue, `Release` to close.
- `Label`: the screen id.
- `DataType`: `menu`, `input` or `display`.
- `FieldType`: `text`, `number`, `decimal` or `phone`, taken from the input screen's type so apps can show the right keyboard.

On `Release` or `Timeout` the session is deleted. Hubtel ignores any reply to a timeout.

**Source:** Hubtel's official [programmable services SDK](https://github.com/hubtel/programmable-services-sdk-dotnet) (request and response models).

## Nalo: `POST /ussd/nalo`

**Request** (JSON):

| Field | Meaning |
| --- | --- |
| `USERID` | Your Nalo user ID |
| `MSISDN` | The caller's number |
| `USERDATA` | The latest input (on the first request, part of the code dialled) |
| `MSGTYPE` | `true` (or `1`) for the first request of a session |
| `NETWORK` | The caller's network |
| `SESSIONID` | The session. If it's missing, the runtime uses `MSISDN`, as Nalo's own sample code does |

**Reply** (JSON):
- `USERID`, `MSISDN` and `USERDATA`, echoed back.
- `MSG`: the text to show. Nalo allows at most 120 characters.
- `MSGTYPE`: `true` to continue, `false` to end.

The flow checker warns about any screen over 120 characters.

**Source:** [Nalo USSD API documentation](https://documenter.getpostman.com/view/7705958/UyrEhaLQ).

## Arkesel: `POST /ussd/arkesel`

**Request** (JSON):
- `sessionID`, `userID`, `msisdn`, `network`.
- `newSession`: `true` on the first request.
- `userData`: the code dialled at first, then the latest input.

**Reply** (JSON):
- `sessionID`, `userID` and `msisdn`, exactly as received (Arkesel requires this).
- `message`.
- `continueSession`: `true` or `false`.

**Source:** [Arkesel API reference](https://developers.arkesel.com/) (USSD section and its OpenAPI file).

## Phone numbers

Gateways write numbers differently: Africa's Talking sends `+233...`, the others send `233...`. The runtime keeps digits only, so `{{phone}}` and the `phone` sent to your backend look the same whichever gateway the call came through.

## Adding a gateway

Implement `gateway.Adapter` (`Parse` and `Write`) in [`internal/gateway`](../internal/gateway/gateway.go) and add it to `All()`. Include a link to the provider's documentation in the pull request.
