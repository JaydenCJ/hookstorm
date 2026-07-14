# Signature schemes

hookstorm signs every delivery exactly the way a real provider would, and the
bundled reference handler verifies with the same code. A "bad" signature in a
storm is therefore bad in precisely the way a provider's tampered or
misconfigured delivery is — which is what makes the `signatures-enforced`
check trustworthy.

All schemes use **HMAC-SHA256** with a lowercase-hex digest and a constant-time
comparison (`hmac.Equal`).

| Scheme | Header | Signed payload | Header value |
|---|---|---|---|
| `github` | `X-Hub-Signature-256` | the raw request body | `sha256=<hex>` |
| `stripe` | `Stripe-Signature` | `<timestamp>.<body>` | `t=<unix>,v1=<hex>` |
| `hex` | `X-Signature` | the raw request body | `<hex>` |

The `github` shape matches GitHub, Shopify, and the many clones that copied it;
`stripe` binds the signature to a timestamp so an old body cannot be replayed
under a new one; `hex` is the bare digest some smaller providers use.

## Signature modes in a storm

When hookstorm builds a plan it labels each delivery with one signature mode.
Only `valid` is expected to be accepted.

| Mode | What hookstorm does | What a correct handler does |
|---|---|---|
| `valid` | signs the real body with the real secret | accept (and de-duplicate) |
| `wrong-key` | signs with a different secret | reject with 4xx |
| `tampered` | signs the real body, then changes a byte in flight | reject with 4xx |
| `missing` | omits the signature header entirely | reject with 4xx |

`--bad-sig` sets the fraction of deliveries that get a broken signature;
`--missing` sets what fraction of those omit the header (the rest split evenly
between `wrong-key` and `tampered`).

## Reproducing a signature by hand

```bash
printf '{"id":"evt_00001","type":"invoice.paid","sequence":1,"data":{"n":1}}' \
  | hookstorm sign --secret whsec_hookstorm --scheme github
# X-Hub-Signature-256: sha256=ac136ad607a161cf9ab247e781aa71f5009139daf70ff6038f8b29d1724c01be
```

The `hex` scheme is verified against RFC 4231 HMAC-SHA256 test case 2
(`secret="Jefe"`, body=`what do ya want for nothing?` →
`5bdcc146…ec3843`) in the test suite, so the MAC is real HMAC-SHA256, not a
homegrown look-alike.
