const std = @import("std");
const bsvz = @import("bsvz");
pub const context = @import("context.zig");

const ParseContext = context.ParseContext;
const ParseResult = context.ParseResult;
const OutPoint = context.OutPoint;
const Opcode = bsvz.script.opcode.Opcode;
const Script = bsvz.script.Script;
const standard = bsvz.script.standard;

pub fn parse1Sat(ctx: *ParseContext) !?ParseResult {
    if (ctx.satoshis != 1) return null;
    var result = ParseResult.init("1sat");
    try result.addEvent(ctx.allocator, "1sat");
    return result;
}

pub fn parseP2PKH(ctx: *ParseContext) !?ParseResult {
    if (ctx.locking_script.len < 25) return null;
    const prefix = Script.init(ctx.locking_script[0..25]);
    if (!standard.isP2PKH(prefix)) return null;
    const pkh = standard.publicKeyHash(prefix) catch return null;

    var result = ParseResult.init("p2pkh");
    try result.addOwner(ctx.allocator, pkh.bytes);
    return result;
}

pub const lock = @import("lock.zig");
pub const inscription = @import("inscription.zig");
pub const cosign = @import("cosign.zig");
// pub const bsv21 = @import("bsv21.zig");
// pub const ordlock = @import("ordlock.zig");
// pub const opns = @import("opns.zig");
// pub const shrug = @import("shrug.zig");
// pub const bitcom = @import("bitcom.zig");

/// Default parser execution order matching Go's DefaultTags.
const default_parsers = [_]context.ParserFn{
    parse1Sat,
    parseP2PKH,
    lock.parse,
    inscription.parse,
    cosign.parse,
};

/// Run all parsers on a single output in order.
pub fn parseOutput(
    allocator: std.mem.Allocator,
    locking_script: []const u8,
    satoshis: u64,
    outpoint: ?OutPoint,
) !ParseContext {
    var ctx = ParseContext.init(allocator, locking_script, satoshis, outpoint);
    errdefer ctx.deinit();

    for (default_parsers) |parser| {
        if (try parser(&ctx)) |result| {
            try ctx.addResult(result);
        }
    }

    return ctx;
}

pub fn main() !void {
    _ = bsvz;
}

// --- Tests ---

test "parse1Sat" {
    const allocator = std.testing.allocator;
    var ctx = ParseContext.init(allocator, &.{}, 1, null);
    defer ctx.deinit();

    var result = (try parse1Sat(&ctx)).?;
    defer result.deinit(allocator);
    try std.testing.expectEqual(@as(usize, 1), result.events.items.len);
    try std.testing.expectEqualStrings("1sat", result.events.items[0]);
}

test "parse1Sat skips non-1sat" {
    const allocator = std.testing.allocator;
    var ctx = ParseContext.init(allocator, &.{}, 100, null);
    defer ctx.deinit();

    const result = try parse1Sat(&ctx);
    try std.testing.expect(result == null);
}

test "parseP2PKH" {
    const allocator = std.testing.allocator;
    var p2pkh: [25]u8 = undefined;
    p2pkh[0] = @intFromEnum(Opcode.OP_DUP);
    p2pkh[1] = @intFromEnum(Opcode.OP_HASH160);
    p2pkh[2] = 20;
    for (3..23) |i| {
        p2pkh[i] = @intCast(i - 3);
    }
    p2pkh[23] = @intFromEnum(Opcode.OP_EQUALVERIFY);
    p2pkh[24] = @intFromEnum(Opcode.OP_CHECKSIG);

    var ctx = ParseContext.init(allocator, &p2pkh, 1000, null);
    defer ctx.deinit();

    var result = (try parseP2PKH(&ctx)).?;
    defer result.deinit(allocator);
    try std.testing.expectEqual(@as(usize, 1), result.owners.items.len);
}

test "parseOutput runs pipeline" {
    const allocator = std.testing.allocator;
    var p2pkh: [25]u8 = undefined;
    p2pkh[0] = @intFromEnum(Opcode.OP_DUP);
    p2pkh[1] = @intFromEnum(Opcode.OP_HASH160);
    p2pkh[2] = 20;
    for (3..23) |i| {
        p2pkh[i] = @intCast(i - 3);
    }
    p2pkh[23] = @intFromEnum(Opcode.OP_EQUALVERIFY);
    p2pkh[24] = @intFromEnum(Opcode.OP_CHECKSIG);

    // 1 sat P2PKH output — should trigger both parsers
    var ctx = try parseOutput(allocator, &p2pkh, 1, null);
    defer ctx.deinit();

    const sat_result = ctx.getResult("1sat");
    try std.testing.expect(sat_result != null);

    const pkh_result = ctx.getResult("p2pkh");
    try std.testing.expect(pkh_result != null);
    try std.testing.expectEqual(@as(usize, 1), pkh_result.?.owners.items.len);
}
