# Neon — Manual test guide (MVP-C)

Step-by-step manual verification for **MVP-C (payment happy path)**.  
Automated coverage: unit **U-C1–U-C6**, integration **I-C1–I-C10** (`go test ./...`).

**Prerequisites**

- Go 1.24+ on `PATH`
- Repo root: `c:\Users\YanSh\Dev\Neon` (adjust paths if different)
- Free port **8080** (or set `API_ADDR`)

---

## 1. Start the server

```powershell
$env:Path = "C:\Program Files\Go\bin;" + $env:Path
cd c:\Users\YanSh\Dev\Neon
go run ./cmd/api
```

Wait until logs show the HTTP server listening (first run may download Temporal CLI ~5–10s).

Open http://localhost:8080

Optional faster hold timer for manual expiry experiments:

```powershell
$env:HOLD_DURATION = "2m"
```

Optional forced payment failures (first N attempts fail):

```powershell
$env:PAYMENT_FAIL_UNTIL = "1"   # first attempt fails, second succeeds
$env:PAYMENT_ALWAYS_FAIL = "1"  # every validation fails (for retry testing)
$env:PAYMENT_NEVER_FAIL = "1"   # never simulate gateway failure
```

Restart `go run ./cmd/api` after changing env vars.

---

## 2. UI flows (recommended)

### 2.1 Happy path (S-1)

| Step | Action | Expected |
|------|--------|----------|
| 1 | Open http://localhost:8080 | Flight list shows **NA4821** and **NA1954** |
| 2 | Click flight **NA4821** | Seat map loads; order created (`localStorage`); timer ~15:00 starts |
| 3 | Click seat **1A** | Status **SEATS_HELD**; timer refreshes ~15:00 |
| 4 | Click **Proceed to payment** | Payment page opens |
| 5 | Enter `12345`, **Submit payment** | Status **CONFIRMED**; confirmation message |
| 6 | **View seat map** | Seat **1A** is **BOOKED** (grayscale) |

### 2.2 Invalid payment code (negative)

| Step | Action | Expected |
|------|--------|----------|
| 1–3 | Same as §2.1 steps 1–3 | **SEATS_HELD** |
| 4 | Payment page: enter `1234` or `abcde` | Inline error; order stays **SEATS_HELD** |
| 5 | Enter valid `12345` | Can still complete payment |

### 2.3 Retry after failure (positive)

| Step | Action | Expected |
|------|--------|----------|
| 1 | Restart API with `$env:PAYMENT_FAIL_UNTIL = "1"` | — |
| 2 | Hold seats, open payment, submit `12345` | Failure message; **SEATS_HELD** |
| 3 | Submit same code again | **CONFIRMED** |

### 2.4 Timer during payment

| Step | Action | Expected |
|------|--------|----------|
| 1 | Hold seats, open payment | Timer counting down |
| 2 | Submit payment; watch status strip | Brief **AWAITING_PAYMENT**; timer **still decreases** |
| 3 | On success | **CONFIRMED** |

---

## 3. API tests with curl (PowerShell)

Base URL: `http://localhost:8080/api/v1`

Helper: save `order_id` from create response.

**Windows note:** If `curl.exe -d '{\"flight_id\":\"NA4821\"}'` returns `invalid request body`, use **single-quoted** JSON (`-d '{"flight_id":"NA4821"}'`) or `Invoke-RestMethod` (examples in §3.11).

### 3.1 Create order and hold seats (setup)

```powershell
$base = "http://localhost:8080/api/v1"

# Create order on flight NA4821
$create = curl.exe -s -X POST "$base/orders" -H "Content-Type: application/json" -d '{\"flight_id\":\"NA4821\"}'
$create
$orderId = ($create | ConvertFrom-Json).order_id

# Hold seat 1A
curl.exe -s -X PATCH "$base/orders/$orderId/seats" -H "Content-Type: application/json" -d '{\"seat_ids\":[\"1A\"]}'
```

**Expected:** `"status":"SEATS_HELD"`, `"held_seat_ids":["1A"]`, `"timer_remaining_seconds"` > 0.

### 3.2 I-C1 / S-1 — Payment happy path (positive)

```powershell
curl.exe -s -X POST "$base/orders/$orderId/payment" -H "Content-Type: application/json" -d '{\"code\":\"12345\"}'
curl.exe -s "$base/orders/$orderId"
curl.exe -s "$base/flights/NA4821/seats"
```

**Expected:**

- Payment response: `"status":"CONFIRMED"`
- Seat map: `1A` has `"status":"BOOKED"`

### 3.3 Invalid code length `1234` (negative)

Use a **new** order (repeat §3.1), then:

```powershell
curl.exe -s -w "\nHTTP %{http_code}\n" -X POST "$base/orders/$orderId/payment" -H "Content-Type: application/json" -d '{\"code\":\"1234\"}'
curl.exe -s "$base/orders/$orderId"
```

**Expected:** HTTP **400**, body `{"error":"invalid payment code"}`; order still **SEATS_HELD**.

### 3.4 Invalid code letters `abcde` (negative)

```powershell
curl.exe -s -w "\nHTTP %{http_code}\n" -X POST "$base/orders/$orderId/payment" -H "Content-Type: application/json" -d '{\"code\":\"abcde\"}'
```

**Expected:** HTTP **400**; order **SEATS_HELD**.

### 3.5 Payment without held seats (negative)

```powershell
$create2 = curl.exe -s -X POST "$base/orders" -H "Content-Type: application/json" -d '{\"flight_id\":\"NA4821\"}'
$orderId2 = ($create2 | ConvertFrom-Json).order_id
curl.exe -s -w "\nHTTP %{http_code}\n" -X POST "$base/orders/$orderId2/payment" -H "Content-Type: application/json" -d '{\"code\":\"12345\"}'
```

**Expected:** HTTP **400**, `"error":"payment not allowed"`; status **CREATED**.

### 3.6 Payment on confirmed order (negative)

After §3.2 success on `$orderId`:

```powershell
curl.exe -s -w "\nHTTP %{http_code}\n" -X POST "$base/orders/$orderId/payment" -H "Content-Type: application/json" -d '{\"code\":\"12345\"}'
```

**Expected:** HTTP **400**, `"error":"payment not allowed"`; order remains **CONFIRMED**.

### 3.7 Unknown order (negative)

```powershell
curl.exe -s -w "\nHTTP %{http_code}\n" -X POST "$base/orders/00000000-0000-0000-0000-000000000099/payment" -H "Content-Type: application/json" -d '{\"code\":\"12345\"}'
```

**Expected:** HTTP **404**, `"error":"order not found"`.

### 3.8 Missing payment body (negative)

```powershell
curl.exe -s -w "\nHTTP %{http_code}\n" -X POST "$base/orders/$orderId/payment" -H "Content-Type: application/json" -d '{}'
```

**Expected:** HTTP **400** (invalid request body).

### 3.9 Retry then succeed (positive, test hook)

Restart API with `$env:PAYMENT_FAIL_UNTIL = "2"`, then new order + hold + three payments:

```powershell
curl.exe -s -X POST "$base/orders/$orderId/payment" -H "Content-Type: application/json" -d '{\"code\":\"12345\"}'
curl.exe -s -X POST "$base/orders/$orderId/payment" -H "Content-Type: application/json" -d '{\"code\":\"12345\"}'
curl.exe -s -X POST "$base/orders/$orderId/payment" -H "Content-Type: application/json" -d '{\"code\":\"12345\"}'
```

**Expected:** First two responses **SEATS_HELD** with failure events; third **CONFIRMED** with ≥3 `payment_events`.

### 3.10 Per-method exhaustion (negative, test hook)

Restart API with `$env:PAYMENT_ALWAYS_FAIL = "1"`, new order + hold, three payments on the same code:

```powershell
1..3 | ForEach-Object {
  curl.exe -s -w "\nHTTP %{http_code}\n" -X POST "$base/orders/$orderId/payment" -H "Content-Type: application/json" -d '{\"code\":\"12345\"}'
}
```

**Expected:** All three return HTTP **200** and **SEATS_HELD** with `methods_used: 1`; order stays active (terminal only after 3 codes × 3 failures — see §6.3 / `TestI_D1`).

### 3.11 Quick smoke script (Invoke-RestMethod)

Run with the API up and `$env:PAYMENT_NEVER_FAIL = "1"` optional:

```powershell
$base = "http://localhost:8080/api/v1"
$o = Invoke-RestMethod -Method POST -Uri "$base/orders" -ContentType "application/json" -Body '{"flight_id":"NA4821"}'
Invoke-RestMethod -Method PATCH -Uri "$base/orders/$($o.order_id)/seats" -ContentType "application/json" -Body '{"seat_ids":["1A"]}'
Invoke-RestMethod -Method POST -Uri "$base/orders/$($o.order_id)/payment" -ContentType "application/json" -Body '{"code":"12345"}'  # CONFIRMED
```

Verified on 2026-05-26: happy path **CONFIRMED**, invalid `1234` → **400**, no seats → **400**, unknown order → **404**, second payment on **CONFIRMED** → **400**.

---

## 4. Sign-off checklist

- [ ] UI happy path (§2.1)
- [ ] UI invalid codes (§2.2)
- [ ] UI retry after failure (§2.3) or API §3.9
- [ ] Timer visible during **AWAITING_PAYMENT** (§2.4)
- [ ] curl happy path (§3.2)
- [ ] curl negative cases (§3.3–3.8, §3.10)
- [ ] `go test ./...` green

When all are checked, confirm **MVP-C** in chat so **MVP-D** can start.

---

## 6. MVP-D — Payment edge cases

Set `$env:PAYMENT_ALWAYS_FAIL = "1"` for failure flows, or `$env:PAYMENT_FAIL_UNTIL = "2"` for partial failures.

### 6.1 UI — New payment method (switch codes mid-method)

1. Hold seats, open payment page.
2. Submit code `11111` once with `PAYMENT_ALWAYS_FAIL=1` (fails).
3. Enter a **different** code `22222` without clicking **Try new payment method**.
4. **Expected:** Inline error / feedback; Submit stays disabled until you click **Try new payment method**.
5. Click **Try new payment method**, enter `22222`, submit — failures reset to `0 / 3 on current code`.

### 6.2 UI — Different code rejected without new-method (U-D6)

1. Submit code `11111` once so it fails (`PAYMENT_ALWAYS_FAIL=1`).
2. Enter a different code `22222` and submit **without** clicking **Try new payment method**.
3. **Expected:** Error feedback; order stays `SEATS_HELD`; same code can still be retried.

### 6.3 UI — Method exhaustion (S-3 / E-E3)

1. With `PAYMENT_ALWAYS_FAIL=1`, fail `11111` three times, then enter `22222` and fail three times, then `33333` three times.
2. **Expected:** Status `PAYMENT_FAILED`; error *"All payment methods failed…"*; seats released; `localStorage` order cleared.

### 6.4 UI — Timer during payment (I-D4)

1. Set `$env:HOLD_DURATION = "2m"` (optional); use a valid code with default RNG.
2. Submit payment and watch the hold timer during `AWAITING_PAYMENT`.
3. **Expected:** Timer keeps counting down (never pauses).

### 6.5 API — New method endpoint

```powershell
$base = "http://localhost:8080/api/v1"
$o = Invoke-RestMethod -Method POST -Uri "$base/orders" -ContentType "application/json" -Body '{"flight_id":"NA4821"}'
Invoke-RestMethod -Method PATCH -Uri "$base/orders/$($o.order_id)/seats" -ContentType "application/json" -Body '{"seat_ids":["1A"]}'
Invoke-RestMethod -Method POST -Uri "$base/orders/$($o.order_id)/payment/new-method" -ContentType "application/json" -Body '{}'
# → 400 if no payment attempts yet; 200 after first method with failures
```

### 6.6 MVP-D sign-off checklist

- [ ] Explicit new-method before code switch (§6.1)
- [ ] Different-code rejection without new-method (§6.2)
- [ ] UI method exhaustion (§6.3)
- [ ] Timer visible during payment (§6.4)
- [ ] `go test ./...` green (U-D1–U-D5, I-D1–I-D10)

When all are checked, confirm **MVP-D** in chat so **MVP-E** can start.

---

## 7. Troubleshooting

| Issue | Fix |
|-------|-----|
| Port 8080 in use | `netstat -ano \| findstr ":8080"` then `Stop-Process -Id <PID> -Force` |
| Temporal connection errors | Ensure `TEMPORAL_AUTO_DEV=1` (default in bootstrap) |
| Payment always fails | Unset `PAYMENT_ALWAYS_FAIL`; or use `PAYMENT_NEVER_FAIL=1` |
| Stale order in browser | Clear `localStorage` key `neon_order_id` or finish/cancel order |
