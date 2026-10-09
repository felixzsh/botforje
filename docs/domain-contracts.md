# Domain contract renames

Target vocabulary; implementation pending.
Rationale and open decisions: [channel-plan.md](channel-plan.md).

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
| `SendRecord.To` | `SendRecord.Recipient` (optional `IdentityKey`) |
| `Chat.ContactID` | `Chat.PeerID` |
| `HistoryChat.ContactKey` / `.ContactName` | `HistoryChat.PeerKey` / `.PeerName` |
| `ContactContent` / `spec.Contact` | `PeerCard` / `spec.PeerCard` |
| `spec.Step.Contact` | `spec.Step.PeerCard` |
| `AllowedContacts` / `IgnoredContacts` | `AllowedPeers` / `IgnoredPeers` |
| Scope-level `Contact.DisplayName` | `Peer.DisplayName` |
| `AddressBookEntry` name fields | Normalized `DirectoryEntry.DisplayName` |

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
