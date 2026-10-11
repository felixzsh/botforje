# Domain contract renames

Target vocabulary; implementation pending.
Status: provisional — entries can change as findings land in
`provider-findings/` and `provider-differences/`, and open questions
live in [open-decisions.md](open-decisions.md).
Rationale: [plan.md](plan.md).

The behavior notes live in that plan's section 2
and the canonical ADRs. This file remains a naming catalog, not final method
signatures or a declaration that the renamed contracts are already sufficient.

`MessageChannel` keeps its name and denotes a logical per-binding view, not
exclusive ownership of a socket/process. `Binding` identifies the fixed verified
association, not the backend's currently selected profile. Neither resource
sharing nor context selection adds a common pool or profile-switch contract.
Exact receive-completion signatures and durable message-provenance fields are
pending decisions; no names or speculative fields are assigned to them here.

## Reading the catalog

A target marked `(redesign)` changes responsibilities, not just spelling. It is
not a mechanical replacement of the current fields, methods, or state values.
The boundaries below settle that distinction; final contracts belong to their
phase's decision review and canonical ADR updates before implementation.
`Retired` means the old contract is not carried forward under a new name.

Internal Go renames do not automatically change YAML/JSON keys, serialized values,
HTTP routes, event topics, or archive formats. Preserve, translate, or deliberately
change each public representation through an explicit compatibility decision;
retaining every old format is not required. No global text replacement constitutes
that decision. The selected `KindContactCard` rename keeps the serialized value
`contact`; other public changes are not approved by this catalog.

## Identity and directory

| Current | Target |
| --- | --- |
| `ContactKey` | `IdentityKey` |
| Scope-level `Contact` | `Peer` |
| `ContactStore` | `PeerStore` |
| `ContactStore.EnsureContact` | `PeerStore.EnsurePeer` |
| `ContactPage` | `PeerPage` |
| `ContactPage.Contacts` | `PeerPage.Peers` |
| `AddressBookEntry` | `DirectoryEntry` |
| `AddressBookStore` | `DirectoryStore` |

[The glossary](../glossary.md) distinguishes peer identity, account-directory
membership, and contact-card payloads; none requires a prior message exchange.
`DirectoryStore` names persistence. Reading or importing an upstream directory is
an optional channel capability with a separate port shape, not decided here.

## Chat and graph execution

| Current | Target |
| --- | --- |
| `Chat`, `ChatStore` | Unchanged |
| `ConversationTarget` | `ChatTarget` |
| `ConversationKind` | `ChatKind` |
| `ConversationDirect` / `ConversationGroup` | `ChatDirect` / `ChatGroup` |
| `Session` / `SessionStore` | `GraphRun` / `GraphRunStore` |
| `SessionID` / `TurnRecord.Session` | `GraphRunID` / `TurnRecord.GraphRun` |
| `HasActiveSessionOutsideNodes` | `HasActiveGraphRunOutsideNodes` |

## Binding

| Current | Target |
| --- | --- |
| `LinkedAccount` | `Binding` |
| `LinkedAccountStore` | `BindingStore` |
| `PairingRunner` | `BindRunner` |
| `PairRequest` | `BindRequest` |
| `PairingSession` | `BindSession` |
| `PairState` | `BindState` |
| `PairingResult` | `BindResult` (redesign) |
| `PairingSession.Code` | `BindSession.NextStep` (`BindStep`) |
| `DevicePresence` | `BindingInspector` (redesign) |
| `PairingStatus` | `BindingStatus` (redesign) |
| `PairingState` | Retired; not renamed to `BindingStatus` |
| `PairingStore` | Retired |
| `AccountUnpairer` | `Unbinder` |
| `UnpairAccount` | `Unbind` |
| `ErrAccountInUse` | `ErrIdentityInUse` |
| `bot pair` / `bot unpair` | `bot bind` / `bot unbind` |

## Messages, cards, and fields

| Current | Target |
| --- | --- |
| `IncomingMessage.Contact` | `IncomingMessage.Sender` (`IdentityKey`) |
| `IncomingMessage.Conversation` | `IncomingMessage.Target` (`ChatTarget`) |
| `OutgoingMessage.RecipientPhone` | `OutgoingMessage.Target` (`ChatTarget`) |
| `OutboundMessage.RecipientPhone` | `ExternalSend.Target` (`ChatTarget`) |
| `OutboundRecord.Recipient` | `OutboundRecord.Target` (`ChatTarget`) |
| `OutboxEntry.Recipient` | `OutboxEntry.Target` (`ChatTarget`) |
| `SendRecord.To` | `SendRecord.Target` (`ChatTarget`) |
| New field, not a rename | `SendRecord.Recipient` (optional `IdentityKey`) |
| `Chat.ContactID` | `Chat.PeerID` |
| `HistoryChat.ContactKey` / `.ContactName` | `HistoryChat.PeerKey` / `.PeerName` |
| `ContactContent` / `spec.Contact` | `ContactCardContent` / `spec.ContactCard` |
| `spec.Step.Contact` | `spec.Step.ContactCard` |
| `ContactEntry` / `spec.ContactEntry` | `ContactCardEntry` / `spec.ContactCardEntry` |
| `spec.ContactEntryKind` | `spec.ContactCardEntryKind` |
| `KindContact` | `KindContactCard` (serialized value stays `contact`) |
| `AllowedContacts` / `IgnoredContacts` | `AllowedPeers` / `IgnoredPeers` |
| Scope-level `Contact.DisplayName` | `Peer.DisplayName` |
| `AddressBookEntry` name fields | Normalized `DirectoryEntry.DisplayName` |

### Outbound addressing

`Target` names the conversation an outbound operation addresses; `Recipient`
is reserved for a known recipient identity, not another name for that destination.
Use `Target` consistently across API send records, turn replies, queued deliveries,
channel sends, and external-send observations. It is the actual destination, which
may differ from the chat whose history records the operation.

`SendRecord.Target` is required; `SendRecord.Recipient` is optional and separate.
A valid target need not identify a known peer, and a shared conversation is not
one person's identity. Do not fabricate a recipient from the thread or derive a
target from the recipient in common code. Provider-specific resolution and the
routing context needed by an authorization remain adapter-owned.

This names the roles, not final field layouts or optional-value representation.
It does not require every outbound contract to duplicate recipient metadata or
change public API/config fields without their compatibility decision. The
contract review belongs to P1 in [plan.md](plan.md).

### Shared contact cards

`ContactCardContent` replaces the proposed `PeerCard` vocabulary.
`ContactCardEntry` names a value inside that payload, not a `DirectoryEntry`.
[The glossary](../glossary.md) owns the distinction between a peer, a directory
entry, and a card describing someone who need not be registered in Botflota.

`KindContactCard` selects this payload. Its serialized value remains `contact`;
card entry labels such as `phone` and `email` retain their values. They are payload
labels, not messaging identities or routing instructions. Each adapter validates
the card variants it can represent. The Go/spec renames do not change YAML/JSON
keys or stored formats without a separate compatibility decision.

## Retired common fields and helpers

| Current | Target |
| --- | --- |
| `LinkedAccount.JID` / `.Account` | Normalized `Binding.Identity` |
| `LinkedAccount.Phone`, `IncomingMessage.FromPhone`, `Contact.Phone` | Removed |
| `AddressBookEntry.Phone`, `HistoryChat.ContactPhone`, `ChatSummary.Phone` | Removed |
| Common `PushName` / `BusinessName` | Removed from domain; adapter-only |
| `RecordInbound` phone parameter | Removed |
| `ConversationTarget.Participant()` / `Contact.DirectConversation()` | Removed |
| Universal `contact.phone` | Removed; legacy alias only |
| `RootContact` | `RootPeer` |
| Runtime identity root `contact` | `peer` |

## Other renames

| Current | Target |
| --- | --- |
| `StateReporter` / `ChannelState` | `TransportReporter` / `TransportState` (redesign) |
| `OutboundMessage` / `OutboundObserver` | `ExternalSend` / `ExternalSendObserver` |
| `HistorySyncer` | `HistorySource` (redesign) |
| `HistoryResult` | `HistoryImportResult` (redesign) |
| `FirstContact` / `FirstContactNotifier` | `PeerFirstSeen` / `PeerFirstSeenNotifier` |
| `FirstContact.Contact` | `PeerFirstSeen.Peer` |
| `EventContactFirstSeen` | `EventPeerFirstSeen` |
| `HistoryTable` / `HistorySink.BeginTable` | `HistorySection` / `HistorySink.BeginSection` |
| `TableChats` | `SectionChats` |
| `TableSessions` | `SectionGraphRuns` |
| `TableMessages` | `SectionMessages` |
| `TableAddressBook` | `SectionDirectory` |
| `HistorySink.Session` / `.AddressBookEntry` | `HistorySink.GraphRun` / `.DirectoryEntry` |
| `api.Fleet.StopBotForUnpair` | `api.Fleet.StopBotForUnbind` |
| `StartPair` / `CurrentPair` / `CancelPair` / `EndPair` | `Begin` / `Get` / `Cancel` / `Wait` |
| `HistoryCounts.Sessions` / `.AddressBook` | `HistoryCounts.GraphRuns` / `.Directory` |

History section names describe domain groupings, not SQL table names or archive
filenames. Renaming constants does not authorize a serialized export-format change.

## Redesign boundaries

- **`BindingInspector` (P3):** local credential presence is one fact, not proof
  of current provider authorization or transport availability. The redesigned
  inspection distinguishes those facts and their unknown or stale observations;
  it does not relabel the current `HasCredentials` boolean as general readiness.
- **`TransportReporter` (P3/P5):** reported states describe transport health.
  Authorization and binding status remain separate. Existing lifecycle states
  cannot be copied unchanged if they mix these meanings.
- **`HistorySource` and `HistoryImportResult` (P5):** source retrieval and traversal
  are distinct from the persisted import outcome. Received entries or source
  exhaustion do not prove storage success. Imported, duplicate, and skipped counts
  belong to the import result, not automatically to the upstream source contract.
- **`BindingStatus` (P3):** describes binding status without treating the old
  paired/not-paired/unpaired enum as the new readiness model. `PairingStatus`
  and `PairingState` were a record and an enum, not interchangeable names.
  The old enum retires; replacement facts and any justified state values are
  decided in P3. `BindState` separately names an interactive attempt's lifecycle.
- **`BindResult` (P3):** reports the binding operation's outcome, not a universal
  phone or completed history import. `PairingResult.Phone` is not carried into
  the common result; verified identity belongs to the neutral binding contract.
  History and directory import metrics are separate operation outcomes, and
  binding commit is distinct from runtime activation. Existing output fields
  require explicit compatibility treatment, not automatic copying into `BindResult`.

These entries close the rename-versus-redesign classification, not the phase's
contract signatures or runtime gates. P3/P5 decisions need not all be resolved
before starting P1.
