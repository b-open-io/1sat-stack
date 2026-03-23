const std = @import("std");
const bsvz = @import("bsvz");
const ctx_mod = @import("context.zig");
const lock_mod = @import("lock.zig");

const ParseContext = ctx_mod.ParseContext;
const ParseResult = ctx_mod.ParseResult;
const ScriptIterator = bsvz.script.parser.ScriptIterator;

pub const OpnsData = struct {
    claimed: []const u8,
    domain: []const u8,
    pow: []const u8,
};

// Full OPNS contract prefix from go-templates/template/opns/contract.go
// This is the compiled sCrypt contract bytecode (~4KB).
const contract = lock_mod.hexDecode(@embedFile("opns_contract.hex"));

// Genesis outpoint: 58b7558ea379f24266c7e2f5fe321992ad9a724fd7a87423ba412677179ccb25_0
// Stored as 36 bytes: txid (32 LE) + vout (4 LE)
const genesis_outpoint: [36]u8 = blk: {
    const txid_hex = "58b7558ea379f24266c7e2f5fe321992ad9a724fd7a87423ba412677179ccb25";
    var txid: [32]u8 = undefined;
    for (0..32) |i| {
        txid[i] = (lock_mod.hexVal(txid_hex[i * 2]) << 4) | lock_mod.hexVal(txid_hex[i * 2 + 1]);
    }
    // Reverse for LE storage
    var reversed: [32]u8 = undefined;
    for (0..32) |i| reversed[i] = txid[31 - i];
    var out: [36]u8 = undefined;
    @memcpy(out[0..32], &reversed);
    std.mem.writeInt(u32, out[32..36], 0, .little);
    break :blk out;
};

/// Matches Go opns.Decode: bytes.HasPrefix(contract), then ReadOp at
/// offset len(contract)+2 for genesis outpoint, claimed, domain, pow.
pub fn parse(ctx: *ParseContext) anyerror!?ParseResult {
    const script = ctx.locking_script;

    if (script.len < contract.len + 10) return null;
    if (!std.mem.startsWith(u8, script, &contract)) return null;

    // Skip contract + 2 bytes (state size), then read ops
    const state_start = contract.len + 2;
    if (state_start >= script.len) return null;

    var iter = ScriptIterator.initBytes(script[state_start..]);

    // Read genesis outpoint
    const genesis_chunk = try iter.next() orelse return null;
    const genesis_data = switch (genesis_chunk) {
        .push_data => |pd| pd.data,
        else => return null,
    };
    if (!std.mem.eql(u8, genesis_data, &genesis_outpoint)) return null;

    // Read claimed
    const claimed_chunk = try iter.next() orelse return null;
    const claimed = switch (claimed_chunk) {
        .push_data => |pd| pd.data,
        else => return null,
    };

    // Read domain
    const domain_chunk = try iter.next() orelse return null;
    const domain = switch (domain_chunk) {
        .push_data => |pd| pd.data,
        else => return null,
    };

    // Read pow
    const pow_chunk = try iter.next() orelse return null;
    const pow = switch (pow_chunk) {
        .push_data => |pd| pd.data,
        else => return null,
    };

    const data = try ctx.allocator.create(OpnsData);
    data.* = .{
        .claimed = claimed,
        .domain = domain,
        .pow = pow,
    };

    var result = ParseResult.init("opns");
    result.data = data;
    try result.addEvent(ctx.allocator, "opns:mine");
    return result;
}

test "parse opns returns null for non-opns script" {
    const allocator = std.testing.allocator;
    var script = [_]u8{ 0x76, 0xa9, 20 } ++ [_]u8{0xaa} ** 20 ++ [_]u8{ 0x88, 0xac };

    var ctx = ParseContext.init(allocator, &script, 1, null);
    defer ctx.deinit();

    try std.testing.expect(try parse(&ctx) == null);
}
