import Foundation

var state = UnreadState()
precondition(state.apply(count: 2, revision: "10"))
precondition(state.apply(count: 1, revision: "11"))
precondition(!state.apply(count: 2, revision: "10"))
precondition(state.count == 1)
precondition(state.apply(count: 0, revision: "12"))
precondition(!state.apply(count: 1, revision: "11"))
precondition(state.count == 0)
precondition(!state.apply(count: -1, revision: "13"))
precondition(!state.apply(count: 5, revision: "invalid"))
precondition(state.apply(count: 1, revision: "13"))
let payload = #"{"id":"one","sender":"test","content":"synthetic","sim":"","source_time":"","received_at":"2026-10-06T00:00:00Z","read":false}"#
var unread = try JSONDecoder().decode(SMSMessage.self, from: Data(payload.utf8))
precondition(unread.isUnread)
let beforeReading = unread
let navigationValue = MessageRoute(message: unread)
unread.read = true
precondition(!unread.isUnread)
precondition(unread != beforeReading) // List must see the renderable state change.
precondition(unread.id == beforeReading.id) // Row identity stays stable.
precondition(MessageRoute(message: unread) == navigationValue)
precondition(Set([MessageRoute(message: unread), navigationValue]).count == 1)
var second = beforeReading
// A separate message fixture verifies that reading one row changes only that row.
let secondPayload = payload.replacingOccurrences(of: "\"one\"", with: "\"two\"")
second = try JSONDecoder().decode(SMSMessage.self, from: Data(secondPayload.utf8))
let beforeRows = [beforeReading, second]
let afterRows = [unread, second]
precondition(beforeRows != afterRows)
precondition(afterRows[0].read == true && afterRows[1].isUnread)
precondition(beforeRows[1] == afterRows[1])
print("Swift unread state: 2 -> 1 -> 0, stale response rejection, new arrival, row value invalidation, independent second row, stable navigation identity PASS")
