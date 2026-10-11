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

## Identity and directory

| Current | Target |
| --- | --- |
| `ContactKey` | `IdentityKey` |
| Scope-level `Contact` | `Peer` |
| `ContactStore` | `PeerStore` |
| `ContactPage` | `PeerPage` |
| `AddressBookEntry` | `DirectoryEntry` |
| `AddressBookStore` | `DirectoryStore` |

## Chat and graph execution

| Current | Target |
| --- | --- |
| `Chat`, `ChatStore` | Unchanged |
| `ConversationTarget` | `ChatTarget` |
| `ConversationKind` | `ChatKind` |
| `ConversationDirect` / `ConversationGroup` | `ChatDirect` / `ChatGroup` |
| `Session` / `SessionStore` | `GraphRun` / `GraphRunStore` |
| `SessionID` / `TurnRecord.Session` | `GraphRunID` / `TurnRecord.Run` |
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
| `PairingResult` | `BindResult` |
| `PairingSession.Code` | `BindSession.NextStep` (`BindStep`) |
| `DevicePresence` | `BindingInspector` |
| `PairingStatus` / `PairingState` | `BindingStatus` (redesigned) |
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
| `ContactContent` / `spec.Contact` | `PeerCard` / `spec.PeerCard` |
| `spec.Step.Contact` | `spec.Step.PeerCard` |
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
| `StateReporter` / `ChannelState` | `TransportReporter` / `TransportState` |
| `OutboundMessage` / `OutboundObserver` | `ExternalSend` / `ExternalSendObserver` |
| `HistorySyncer` / `HistoryResult` | `HistorySource` / `HistoryImportResult` |
| `FirstContact` / `FirstContactNotifier` | `PeerFirstSeen` / `PeerFirstSeenNotifier` |
| `EventContactFirstSeen` | `EventPeerFirstSeen` |
| `HistoryTable` / `HistorySink.BeginTable` | `HistorySection` / `HistorySink.BeginSection` |
| `HistorySink.Session` / `.AddressBookEntry` | `HistorySink.Run` / `.DirectoryEntry` |
| `api.Fleet.StopBotForUnpair` | `api.Fleet.StopBotForUnbind` |
| `StartPair` / `CurrentPair` / `CancelPair` / `EndPair` | `Begin` / `Get` / `Cancel` / `Wait` |
| `HistoryCounts.Sessions` / `.AddressBook` | `HistoryCounts.Runs` / `.Directory` |
