# WhatsApp Cloud API adapter findings

Recorded: 2026-10-09. Outbound API research follows; setup and implementation are pending.
[ROADMAP](../../../ROADMAP.md), Phase 18, lists `adapters/whatsappcloud` as a future channel.
This entry preserves its existing context and an outbound capability inventory. No concrete SDK,
setup flow, deployment, or resource benchmark was selected.

## Existing direction

- This is another channel reaching WhatsApp, not a new messaging provider or an extension of
  the native whatsmeow device model.
- The roadmap keeps it behind the same common messaging contracts. Onboarding and pricing
  differ from the native channel; graph behavior does not become Cloud API-specific.
- The owner's browser-free, direct-integration preference should inform the future investigation.
  It does not establish a selected library or a finalized deployment here.

## Revisit when implementing

- Investigate official setup, credential custody, inbound delivery, outbound acceptance, and
  implemented capabilities rather than inheriting native-client assumptions.
- Evaluate direct Go integration before introducing another language runtime or SDK dependency.
- Verify provider policy, pricing, limits, and eligibility at that time.
- Resolve any new credential requirements under ADR 0053.
- Measure the selected path; do not extrapolate RAM, CPU, or 1000-account capacity from whatsmeow.

No Cloud API library, resource estimate, new dependency, or completed plan gate was established
by this conversation.

## Outbound send contract

Reviewed on 2026-10-09 against current official documentation, whose service-send examples use
Graph API v25.0. See the [outbound matrix](../provider-differences/outbound-messages.md).
Direct HTTPS from Go is
sufficient; this research does not select a wrapper, implement a channel or certify live delivery.

### Common envelope and eligibility

Call `POST https://graph.facebook.com/<version>/<phone-number-id>/messages` with bearer auth.
Required ordinary send fields are `messaging_product="whatsapp"`, destination `to`, `type`,
and the corresponding content object; `recipient_type="individual"` selects the 1:1 case here.
The phone-number ID names the sending business account resource, not the destination.

Current documentation also describes business-scoped recipient IDs (`recipient`) on some
operations. Do not generalize a universal phone requirement or promise the same recipient form
on every message endpoint without checking the chosen Graph version and account enablement.

- Free-form service sends require an open **24-hour customer service window** and applicable
  opt-in/eligibility. Outside it, use an approved template. Offline shape validation cannot
  establish that a free-form send is usable now.
- Media uses an uploaded `id` **or** public `link`, not both. Upload via the media endpoint with
  file/MIME metadata first; a process-local path cannot be placed in the message's `link`.
- Optional contextual reply uses `context.message_id` from the conversation.
- Response `messages[].id` means accepted, not delivered. Status webhooks report the outcome.
  Even sequential sends need not be delivered in request order.

### Text, media, location, and contacts

- **Text/styled text:** `type="text"`, `text.body` (maximum 4096 characters); optional
  `text.preview_url`. WhatsApp inline formatting is in the body, not an entity-offset array.
- **Link preview:** URL in `text.body`, `preview_url=true`; preview fetching/rendering is best
  effort, for the first qualifying URL. Failure to generate it does not make a new message kind.
- **Image:** `type="image"`, `image.id` or `image.link`; optional `caption` (1024 characters).
  JPEG/PNG, 8-bit RGB/RGBA, at most 5 MB.
- **Video:** `type="video"`, `video.id` or `video.link`; optional `caption` (1024 characters).
  MP4/3GPP up to 16 MB, H.264 video and AAC audio, one audio stream or none.
- **Audio:** `type="audio"`, `audio.id` or `audio.link`; no general caption field.
  AAC/AMR/MP3/M4A and supported Ogg/Opus, at most 16 MB.
- **Voice note:** the audio payload with **`audio.voice=true`**; Ogg/Opus required.
  This explicit field is present in the current official API; old claims that Cloud cannot
  choose voice presentation are outdated. Default/false gives basic audio presentation.
- **Document:** `type="document"`, `document.id` or `document.link`; optional `filename`,
  `caption` (1024 characters). Officially supported text, PDF and listed office formats up to
  100 MB; other formats can be accepted without a display/handling guarantee.
- **Sticker:** `type="sticker"`, `sticker.id` or `sticker.link`; WebP, 512×512.
  Static up to 100 KB, animated up to 500 KB. No caption. There is no separate GIF message
  type; sending video/animated WebP is a different declared presentation.
- **Location / named place:** `type="location"`, `location.latitude`, `longitude`;
  optional `name`, `address`. This is a static map pin, not a live-location session.
- **Contact cards:** `type="contacts"`, `contacts[]`; each needs `name.formatted_name` in the
  current page. Structured name parts are optional there. Optional `phones[]` (`phone`, `type`,
  `wa_id`), `emails[]`, `addresses[]`, `urls[]`, `org`, `birthday` (`YYYY-MM-DD`).
  Up to 257 contacts in the reviewed documentation, but sending fewer is recommended.
  Neither a required phone number nor a raw vCard is the Cloud card contract. Client support
  for metadata can vary. This does not register those people as Botflota peer identities.

### Reply buttons

Use `type="interactive"` with `interactive.type="button"`:

- Required `body.text` (up to 1024 characters).
- Required `action.buttons[]`, 1–3 entries; each `{type: "reply", reply: {id, title}}`.
  `id`: up to 256 characters; `title`: up to 20, unique among the message's buttons.
- Optional `header` (text/image/video/document), `footer.text` (up to 60).
  Media headers need a media ID or URL.
- Response webhook carries `interactive.button_reply.id` and `title`. The stable value is
  the ID, not text reconstructed from the label. Transport webhook acknowledgement is separate
  from processing that selection in a graph.

### Single-select list

Use `interactive.type="list"`:

- Required `body.text`, `action.button` (open-list label, up to 20 characters),
  `action.sections[]`, each containing `title` and `rows[]`.
- Each row needs `id` (up to 200 characters), `title` (up to 24); optional `description`
  (up to 72). Section titles are up to 24 characters.
- Up to 10 sections and **10 total rows**, not ten per section. One selected row per response.
- Optional text-only header (60 characters) and footer (60).
- The current list-specific page states a 4096-character body limit, unlike the reply-button
  page's 1024. Generic/older interactive references can differ: preserve this evidence and
  verify against the pinned Graph version rather than treating 4096 as a universal limit.
- Webhook `interactive.list_reply` supplies the selected `id`, `title`, optional description.

### Other interactive actions

- **URL button:** `interactive.type="cta_url"`, required `body.text`,
  `action.name="cta_url"`, `action.parameters.display_text` (20 characters), `url`.
  Optional text/media header and footer. Opening a URL is not a reply-button selection.
- **Location request:** `interactive.type="location_request_message"`, `body.text`
  (1024 characters), `action.name="send_location"`. The user consents and shares coordinates;
  the request contains no bot-authored coordinate payload.
- **Address request:** `interactive.type="address_message"`, body and action parameters
  including required `country="IN"`; only India-based businesses and India customers at this
  baseline. Optional prefilled `values`, saved addresses and validation-error data. It requests
  a structured delivery address, not a universally available contact/location card.
- **Flow/form:** `interactive.type="flow"`, `body.text`, `action.name="flow"` and
  `parameters.flow_message_version="3"`, `flow_cta`, exactly one of `flow_id` / `flow_name`.
  Optional `mode` (`published` default / `draft`), `flow_token`, `flow_action`
  (`navigate` default / `data_exchange`), `flow_action_payload.screen` / `.data`.
  Flow assets, publication, recipient compatibility and any encrypted data-exchange endpoint
  are separate requirements. This is not a generic arbitrary inline form object.
- **Call button:** `interactive.type="voice_call"`, `body.text`, `action.name="voice_call"`;
  optional parameters `display_text` (20 characters), `ttl_minutes` (1–43200, default 10080),
  `payload` (512 characters). Requires Cloud Calling enablement; the button invites an inbound
  call to the business, it does not send voice/video call media via the message API.

### Commerce and regional payment messages

All ordinary commerce variants use the common send envelope and `type="interactive"`.
Catalog linkage and account eligibility are prerequisites, not an arbitrary common product ID.

- **Single product:** `interactive.type="product"`, required `action.catalog_id`,
  `action.product_retailer_id`; optional body/footer. It references catalog inventory; product
  details are fetched from that inventory, not supplied as a stand-alone text/photo card.
- **Product list:** `interactive.type="product_list"`, required text `header`, `body`,
  `action.catalog_id`, `action.sections[]` with `title`, `product_items[]` containing
  `product_retailer_id`. Up to 30 products; optional footer.
- **Catalog:** `interactive.type="catalog_message"`, `body.text`,
  `action.name="catalog_message"`; optional `parameters.thumbnail_product_retailer_id`, footer.
- **Payment/order request:** regional `interactive.type="order_details"`, body,
  `action.name="review_and_pay"`. The reviewed Brazil shape requires `reference_id`, `type`,
  `payment_type="br"`, `payment_settings[]`, `currency="BRL"`, `total_amount: {value, offset}`;
  optional itemized `order`. India uses a different eligible flow with `INR`, payment setup,
  order/items/subtotal and other fields; the examined payment-link page warns it is not publicly
  available. There is no universal `sendInvoice` equivalent that can be inferred from these.

### Approved templates, including copy code and carousels

Sending, not creating, uses `type="template"` and:

- Required `template.name`, `template.language.code` naming an approved localized template.
- `template.components[]` when the template needs dynamic header/body/button values.
  Each component identifies `type`, applicable `sub_type` / button `index`, and `parameters[]`.
  Parameter variants include text, media, currency/date values, action data and button payloads.
  Named parameters must match the template's declared names; positional values must match order.
- A URL/quick-reply/Flow/call button's definition and label belong to the approved template.
  Sending supplies only the dynamic values allowed by that definition.
- **Copy-code coupon:** button component `sub_type="copy_code"`, button `index`, parameter
  `{type: "coupon_code", coupon_code: <value>}` (maximum 20 characters). Not a generic free-form
  clipboard button; label is fixed, one copy-code button, not supported by WhatsApp Web here.
- **Media carousel:** component `type="carousel"`, `cards[]` with `card_index`, `components[]`.
  Each card supplies required image/video header parameters and any declared body/button values:
  quick-reply `payload`, URL-variable text, etc. Define 2–10 cards; the send count must match the
  approved template's count, with the same component layout across cards.
- **Product carousel:** the same carousel/card indexing, with header parameter `type="product"`,
  `product: {product_retailer_id, catalog_id}` and applicable button values. The reviewed product
  guide permits an approved two-card template to send up to 10 product cards, unlike the media
  template count rule. Neither variant is an arbitrary generic rich-block payload.
- Template sending outside the service window still depends on approval/status, policy, account
  eligibility and pricing. A registered template name is not sufficient proof of deliverability.

### Quotes, reactions, and absent operations

- **Quote:** `context: {message_id}` plus normal content; same-conversation restrictions apply.
- **Reaction:** `type="reaction"`, `reaction.message_id`, `reaction.emoji`; empty emoji removes
  the reaction. The reviewed page rejects nonexistent/deleted/reaction targets or targets older
  than 30 days. It emits a sent status, not delivery/read status for the reaction itself.
- No native poll/quiz, live location, round video, view-once, arbitrary file album, edit/revoke,
  server-side forward, silent-notification send or future scheduling was established in the
  reviewed service API. Resending content is not forwarding with original provenance.
- Delivery TTL (retry validity, normally 30 days) is **not** recipient-visible disappearing
  content. Template validity customization does not implement after-read expiry or scheduling.

## Sources

Official pages reviewed on 2026-10-09. Long page paths below are relative to the
[official WhatsApp documentation root][root]; the [full index][index] links each named page.

- [Service sending][service] and [audio/voice][audio], including the current `voice` parameter.
- Under `messages/`: `text-messages`, `image-messages`, `video-messages`, `document-messages`,
  `sticker-messages`, `contacts-messages`, `location-messages`, `reaction-messages`.
- Under `messages/`: `interactive-reply-buttons-messages`, `interactive-list-messages`,
  `interactive-cta-url-messages`, `location-request-messages`, `address-messages`.
- [Sending a Flow][flow]; `calling/call-button-messages-deep-links`.
- Under `catalogs/`: `share-products`, `single-product-messages`, `multi-product-messages`,
  `catalog-messages`, `product-card-carousel-template-messages`.
- [Template fundamentals][templates]; under `templates/marketing-templates/`:
  `coupon-templates`, `media-card-carousel-templates`.
- Regional examples: `payments/payments-br/orders`, `payments/payments-in/payment-links`.

[root]: https://developers.facebook.com/documentation/business-messaging/whatsapp/
[index]: https://developers.facebook.com/documentation/business-messaging/whatsapp/llms.txt
[service]:
  https://developers.facebook.com/documentation/business-messaging/whatsapp/messages/send-messages
[audio]:
  https://developers.facebook.com/documentation/business-messaging/whatsapp/messages/audio-messages
[flow]:
https://developers.facebook.com/documentation/business-messaging/whatsapp/flows/guides/sendingaflow
[templates]:
  https://developers.facebook.com/documentation/business-messaging/whatsapp/templates/overview
