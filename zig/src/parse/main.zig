const std = @import("std");
const bsvz = @import("bsvz");

pub const ParseResult = struct {
    allocator: std.mem.Allocator,
    events: std.ArrayListUnmanaged([]const u8),
    owners: std.ArrayListUnmanaged([20]u8),

    pub fn init(allocator: std.mem.Allocator) ParseResult {
        return .{
            .allocator = allocator,
            .events = .empty,
            .owners = .empty,
        };
    }

    pub fn addEvent(self: *ParseResult, event: []const u8) !void {
        try self.events.append(self.allocator, event);
    }

    pub fn addOwner(self: *ParseResult, pkhash: [20]u8) !void {
        try self.owners.append(self.allocator, pkhash);
    }

    pub fn deinit(self: *ParseResult) void {
        self.events.deinit(self.allocator);
        self.owners.deinit(self.allocator);
    }
};

/// Check if output is exactly 1 satoshi.
pub fn parse1Sat(satoshis: u64, result: *ParseResult) !void {
    if (satoshis == 1) {
        try result.addEvent("1sat");
    }
}

/// Extract P2PKH address from locking script.
pub fn parseP2PKH(locking_script: []const u8, result: *ParseResult) !void {
    if (locking_script.len != 25) return;
    if (locking_script[0] != 0x76) return; // OP_DUP
    if (locking_script[1] != 0xa9) return; // OP_HASH160
    if (locking_script[2] != 20) return; // push 20 bytes
    if (locking_script[23] != 0x88) return; // OP_EQUALVERIFY
    if (locking_script[24] != 0xac) return; // OP_CHECKSIG

    var pkhash: [20]u8 = undefined;
    @memcpy(&pkhash, locking_script[3..23]);
    try result.addOwner(pkhash);
}

/// Run all parsers on a single output.
pub fn parseOutput(allocator: std.mem.Allocator, locking_script: []const u8, satoshis: u64) !ParseResult {
    var result = ParseResult.init(allocator);
    errdefer result.deinit();

    try parse1Sat(satoshis, &result);
    try parseP2PKH(locking_script, &result);

    return result;
}

pub fn main() !void {
    _ = bsvz;
}

test "parse1Sat" {
    var result = ParseResult.init(std.testing.allocator);
    defer result.deinit();

    try parse1Sat(1, &result);
    try std.testing.expectEqual(@as(usize, 1), result.events.items.len);
    try std.testing.expectEqualStrings("1sat", result.events.items[0]);
}

test "parse1Sat skips non-1sat" {
    var result = ParseResult.init(std.testing.allocator);
    defer result.deinit();

    try parse1Sat(100, &result);
    try std.testing.expectEqual(@as(usize, 0), result.events.items.len);
}

test "parseP2PKH" {
    var result = ParseResult.init(std.testing.allocator);
    defer result.deinit();

    var p2pkh: [25]u8 = undefined;
    p2pkh[0] = 0x76;
    p2pkh[1] = 0xa9;
    p2pkh[2] = 20;
    for (3..23) |i| {
        p2pkh[i] = @intCast(i - 3);
    }
    p2pkh[23] = 0x88;
    p2pkh[24] = 0xac;

    try parseP2PKH(&p2pkh, &result);
    try std.testing.expectEqual(@as(usize, 1), result.owners.items.len);
}

test "parseP2PKH rejects non-p2pkh" {
    var result = ParseResult.init(std.testing.allocator);
    defer result.deinit();

    const not_p2pkh = [_]u8{ 0x00, 0x01, 0x02 };
    try parseP2PKH(&not_p2pkh, &result);
    try std.testing.expectEqual(@as(usize, 0), result.owners.items.len);
}
