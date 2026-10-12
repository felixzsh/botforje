# Domain contract renames

Selected vocabulary; P1 identity/target propagation and P3 binding/setup
contracts are implemented. P4 public naming was owner-approved on 2026-10-12:
HTTP resources, CLI commands and arguments, JSON fields, counts, and exports
match these domain terms. Card and graph-run code renames and P4's public
consumers remain implementation work; history-source and external-send behavior
still belongs to P5. Open behavioral questions live in
[open-decisions.md](open-decisions.md).
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

Source baseline: `e6f6de0`. Every table separates the actual code, the selected
target term, and the nature of the change. Unqualified symbols belong to the core;
an unqualified target method belongs to its renamed owner. CLI names and template
paths are identified explicitly. Names from earlier proposals are not current code.

An empty `Current code` cell means there is no directly corresponding declaration
or field in that baseline. It does not mean the runtime has no related behavior.
A new neutral type can model an existing role currently represented by strings or
flags. `New concept` is reserved for something without an existing conceptual
analogue; missing a named type alone does not justify that classification.

`Rename` preserves the concept. `Rename + redesign` changes the name and also its
representation or responsibilities; it is not a mechanical symbol replacement.
`No rename; redesign` keeps the name but changes the contract. `Retire` has no
replacement name, shown as `—`. The boundaries below settle these distinctions,
not final layouts, signatures, or state values: those belong to the affected
phase's canonical ADRs before implementation. P3's accepted contracts override
older provisional bind-session spellings below; no second set of public setup
names is introduced for them.

Internal Go renames do not themselves define wire formats. P4's approved public
contracts live in ADR 0042/0044/0052/0053/0054 and use the selected domain
vocabulary, not mechanically every old code symbol. There is no compatibility
translation, alias, or dual-format period. `KindContactCard` and the selected
card declaration keep the serialized kind/key `contact` and payload label
`phone` (ADR 0036); neither names an identity or destination. Event naming is
already `peer.first_seen` (ADR 0045).

## Identity and directory

| Current code | Target term | Change |
| --- | --- | --- |
| | `IdentityKey` | New type; existing identity role |
| `Contact` | `Peer` | Rename + identity redesign |
| `Contact.Phone` | `Peer.Key` (`IdentityKey`) | Rename + identity redesign |
| `ContactStore` | `PeerStore` | Rename + identity redesign |
| `ContactStore.EnsureContact` | `PeerStore.EnsurePeer` | Rename + identity redesign |
| `ContactPage` | `PeerPage` | Rename; follows peer redesign |
| `ContactPage.Contacts` | `PeerPage.Peers` | Rename |
| `Peer.AddressBookImported` | `Peer.DirectoryImported` | Align recorded-import terminology |
| `AddressBookEntry` | `DirectoryEntry` | Rename + directory redesign |
| `AddressBookEntry.Phone` | `DirectoryEntry.PeerKey` | Rename + identity redesign |
| | `DirectoryEntry.SourceIdentity` | New field; historical account origin |
| `AddressBookStore` | `DirectoryStore` | Rename + directory redesign |

[The glossary](../glossary.md) distinguishes peer identity, account-directory
membership, and contact-card payloads; none requires a prior message exchange.
`DirectoryStore` names persistence. Reading or importing an upstream directory is
an optional channel capability with a separate port shape, not decided here.

## Chat and graph execution

| Current code | Target term | Change |
| --- | --- | --- |
| `Chat`, `ChatStore` | Unchanged | No rename; addressing redesign |
| `ChatStore.ForContact` | `ChatStore.ForTarget` | Rename + target lookup redesign |
| | `ChatTarget` | New type; existing addressing role |
| | `ChatKind` | New type; generalizes group flag |
| | `ChatDirect` / `ChatGroup` | New constants; existing distinction |
| `Session` / `SessionStore` | `GraphRun` / `GraphRunStore` | Rename |
| `Message.SessionID` | `Message.GraphRunID` | Rename |
| `TurnRecord.Session` | `TurnRecord.GraphRun` | Rename |
| `SessionStore.HasActiveSessionOutsideNodes` | `HasActiveGraphRunOutsideNodes` | Rename |

## Binding

| Current code | Target term | Change |
| --- | --- | --- |
| `LinkedAccount` | `Binding` | Rename + binding redesign |
| `LinkedAccountStore` | `BindingStore` | Rename + binding redesign |
| `PairingRunner` | `SetupRunner` | P3 contract; public consumers change in P4 |
| `PairRequest` | `SetupBegin` | P3 contract; not a universal QR request |
| `PairingSession` | `SetupSession` | P3 contract; typed, revisioned steps |
| `PairState` | `SetupState` | P3 contract; setup attempt, not readiness |
| `PairingResult` | `Binding` + setup outcome | Separate commit, activation, and import |
| `PairingSession.Code` | `SetupSession.Step.Display` | Expiring display challenge |
| | `SetupStep` | P3 typed display/input/approval/profile step |
| `DevicePresence` | `AccountLifecycle.InspectBinding` | Readiness, not just presence |
| `PairingStatus` | `BindingStatus` | Rename + readiness redesign |
| `PairingState` | — | Retire old pairing enum |
| `PairingStore` | — | Retire |
| `AccountUnpairer` | `AccountLifecycle` | P3 inspection/detachment contract |
| `AccountUnpairer.UnpairAccount` | `DetachBinding` | Local detachment, not destruction |
| `ErrAccountInUse` | `ErrIdentityInUse` | Rename + identity redesign |
| `bot pair` / `bot unpair` | `bot bind` / `bot unbind` | Approved P4; no old alias |

## Messages, cards, and fields

| Current code | Target term | Change |
| --- | --- | --- |
| `IncomingMessage.FromPhone` | `IncomingMessage.Sender` (`IdentityKey`) | Rename + redesign |
| | `IncomingMessage.Target` (`ChatTarget`) | New field; existing addressing role |
| `IncomingMessage.IsGroup` | Kind on `Target` | Redesign; neutral conversation kind |
| `OutgoingMessage.RecipientPhone` | `OutgoingMessage.Target` | Rename + addressing redesign |
| `OutboundMessage.RecipientPhone` | `ExternalSend.Target` | Rename + addressing redesign |
| `OutboundRecord.Recipient` | `OutboundRecord.Target` | Rename + addressing redesign |
| `OutboxEntry.Recipient` | `OutboxEntry.Target` | Rename + addressing redesign |
| `SendRecord.To` | `SendRecord.Target` | Rename + addressing redesign |
| | `SendRecord.Recipient` (`IdentityKey`) | New field; separates identity from target |
| `Chat.ContactID` | `Chat.PeerID` | Rename + participant redesign |
| `HistoryChat.ContactPhone` | `HistoryChat.PeerKey` | Rename + identity redesign |
| `HistoryChat.ContactName` | `HistoryChat.PeerName` | Rename |
| `ContactContent` / `spec.Contact` | `ContactCardContent` / `spec.ContactCard` | Rename |
| `spec.Step.Contact` | `spec.Step.ContactCard` | Rename; serialized key unchanged |
| `ContactEntry` / `spec.ContactEntry` | `ContactCardEntry` / `spec.ContactCardEntry` | Rename |
| `spec.ContactEntryKind` | `spec.ContactCardEntryKind` | Rename; labels unchanged |
| `KindContact` | `KindContactCard` | Rename; value remains `contact` |
| `spec.BotSettings.AllowedContacts` | `spec.BotSettings.AllowedPeers` | Rename + redesign |
| `spec.BotSettings.IgnoredContacts` | `spec.BotSettings.IgnoredPeers` | Rename + redesign |
| `Contact.DisplayName` | `Peer.DisplayName` | Rename |
| `AddressBookEntry.DisplayName()` | `DirectoryEntry.DisplayName` | Adapter normalization |

`IncomingMessage.FromPhone` currently carries a phone string, not an identity key.
Its replacement needs adapter normalization to `Sender`, not just a name change.
The inbound `Target` is new; it needs explicit conversation addressing beyond the
current `IsGroup` flag. `HistoryChat.ContactPhone` already serves an identity role;
`PeerKey` generalizes that projection rather than merely renaming a phone value.
All `Target` entries use `ChatTarget`; known `Sender` and `Recipient` identities use
`IdentityKey`. The selected P1 shapes and optional-value conventions now live in
[ADR 0051](../adr/0051-channel-neutral-identity-and-conversations.md); P1
implements them, and ADR 0044 selects their P4 public references.

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

[ADR 0051](../adr/0051-channel-neutral-identity-and-conversations.md) fixes the P1
value types: `SendRecord.Recipient` uses `*IdentityKey`, with nil for unknown;
`Target` is a required value. Other outbound contracts need not duplicate recipient
metadata. ADR 0044 now selects the neutral P4 public API selectors; approval
of those shapes does not implement their handlers.

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

| Current code | Target term | Change |
| --- | --- | --- |
| `LinkedAccount.JID` | `Binding.Identity` | Redesign; verified account identity |
| `LinkedAccount.Phone()` | — | Retire universal phone getter |
| `ChatSummary.Phone` | — | Retire phone-only projection |
| `AddressBookEntry.PushName` / `.BusinessName` | — | Adapter-only; normalize directory name |
| `ContactStore.RecordInbound` phone parameter | — | Retire phone parameter |
| `contact.phone` template path | — | Retired; no alias |
| `RootContact` | `RootPeer` | P1 neutral root |
| `contact` runtime root | `peer` | Implemented; no provider translation |

## Other renames

| Current code | Target term | Change |
| --- | --- | --- |
| `StateReporter` | `TransportReporter` | Rename + transport redesign |
| `StateReporter.ChannelState` | `TransportState` | Rename + transport redesign |
| `OutboundMessage` | `ExternalSend` | Rename + provenance redesign |
| `OutboundObserver` | `ExternalSendObserver` | Rename + provenance redesign |
| `HistorySyncer` | `HistorySource` | Rename + retrieval redesign |
| `HistoryResult` | `HistoryImportResult` | Rename + import-result redesign |
| `FirstContact` | `PeerFirstSeen` | Rename |
| `FirstContactNotifier` | `PeerFirstSeenNotifier` | Rename |
| `FirstContact.Contact` | `PeerFirstSeen.Peer` | Rename |
| `EventContactFirstSeen` | `EventPeerFirstSeen` | Implemented; topic `peer.first_seen` |
| `HistoryTable` | `HistorySection` | Rename + grouping redesign |
| `HistorySink.BeginTable` | `HistorySink.BeginSection` | Rename + grouping redesign |
| `TableChats` | `SectionChats` | P4 domain section; format in ADR 0042 |
| `TableSessions` | `SectionGraphRuns` | P4 domain section; format in ADR 0042 |
| `TableMessages` | `SectionMessages` | P4 domain section; format in ADR 0042 |
| `TableAddressBook` | `SectionDirectory` | P4 domain section; format in ADR 0042 |
| `HistorySink.Session` | `HistorySink.GraphRun` | Rename |
| `HistorySink.AddressBookEntry` | `HistorySink.DirectoryEntry` | Rename; directory redesign |
| `api.Fleet.StopBotForUnpair` | `StopBotForUnbind` | Rename; binding lifecycle applies |
| `PairingRunner.StartPair` | `SetupRunner.BeginSetup` | P3; P4 public consumer |
| `PairingRunner.CurrentPair` | `SetupRunner.ReadSetup` | P3 lookup by setup ID |
| `PairingRunner.CancelPair` | `SetupRunner.CancelSetup` | P3 cancellation by setup ID |
| `PairingRunner.EndPair` | — | Completion wait owned by detach implementation |
| `HistoryCounts.Sessions` | `HistoryCounts.GraphRuns` | Rename |
| `HistoryCounts.AddressBook` | `HistoryCounts.Directory` | Rename |

History section names describe domain groupings, not SQL tables or filenames.
ADR 0042 owns the approved P4 ZIP/CSV format; stores expose domain sections.

## Redesign boundaries

- **`AccountLifecycle.InspectBinding` (P3):** local credential presence is one fact, not proof
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
  The old enum retires from the P4 public contract; P3 readiness conditions
  already separate the facts. `SetupState` names an interactive attempt's state.
- **Setup outcome (P3/P4):** `Binding` carries the verified identity;
  `SetupSession` reports setup outcome and a separate activation failure. No
  universal phone or completed import defines success. History and directory
  import metrics belong to their own operations, never copied into a public
  compatibility result.

These entries close the rename-versus-redesign classification, not the phase's
contract signatures or runtime gates. P3/P5 decisions need not all be resolved
before starting P1.
