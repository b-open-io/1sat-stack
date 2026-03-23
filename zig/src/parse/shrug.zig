const std = @import("std");
const bsvz = @import("bsvz");
const ctx_mod = @import("context.zig");

const ParseContext = ctx_mod.ParseContext;
const ParseResult = ctx_mod.ParseResult;
const OutPoint = ctx_mod.OutPoint;
const ScriptIterator = bsvz.script.parser.ScriptIterator;
const Opcode = bsvz.script.opcode.Opcode;

const shrug_tag = "\xc2\xaf\\_(\xe3\x83\x84)_/\xc2\xaf"; // ¯\_(ツ)_/¯ in UTF-8

pub const ShrugData = struct {
    id: ?OutPoint,
    script_suffix: []const u8,
};

/// Matches Go shrug.Decode: read PUSH(shrug_tag), PUSH(36-byte outpoint or OP_0),
/// OP_2DROP, PUSH(amount), OP_DROP, then capture suffix.
pub fn parse(ctx: *ParseContext) anyerror!?ParseResult {
    var iter = ScriptIterator.initBytes(ctx.locking_script);

    // First: PUSH with shrug tag (9 bytes including the trailing byte from Go)
    // Go checks: len(op.Data) != 9 || string(op.Data[:8]) != SHRUG_TAG
    // SHRUG_TAG is "¯\_(ツ)_/¯" which is 13 bytes in UTF-8
    const tag_chunk = try iter.next() orelse return null;
    const tag_data = switch (tag_chunk) {
        .push_data => |pd| pd.data,
        else => return null,
    };
    if (tag_data.len < shrug_tag.len or !std.mem.startsWith(u8, tag_data, shrug_tag)) return null;

    // Second: outpoint (36 bytes) or OP_0 (no id)
    const id_chunk = try iter.next() orelse return null;
    var id: ?OutPoint = null;
    switch (id_chunk) {
        .push_data => |pd| {
            if (pd.data.len == 36) {
                id = .{
                    .txid = .{ .bytes = pd.data[0..32].* },
                    .index = std.mem.readInt(u32, pd.data[32..36], .little),
                };
            }
        },
        .opcode => |op| {
            if (op != .OP_0) return null;
        },
        else => return null,
    }

    // OP_2DROP
    const drop2_chunk = try iter.next() orelse return null;
    switch (drop2_chunk) {
        .opcode => |op| if (op != .OP_2DROP) return null,
        else => return null,
    }

    // Amount (ScriptNumber) — we skip parsing the numeric value for now
    const amt_chunk = try iter.next() orelse return null;
    _ = amt_chunk;

    // OP_DROP
    const drop_chunk = try iter.next() orelse return null;
    switch (drop_chunk) {
        .opcode => |op| if (op != .OP_DROP) return null,
        else => return null,
    }

    const suffix = ctx.locking_script[iter.pos()..];

    const data = try ctx.allocator.create(ShrugData);
    data.* = .{
        .id = id,
        .script_suffix = suffix,
    };

    var result = ParseResult.init("shrug");
    result.data = data;

    if (id) |outpoint| {
        // Existing token — emit id event
        var reversed: [32]u8 = undefined;
        for (&reversed, 0..) |*b, i| b.* = outpoint.txid.bytes[31 - i];
        var hex_buf: [64]u8 = undefined;
        for (&reversed, 0..) |b, i| {
            hex_buf[i * 2] = "0123456789abcdef"[b >> 4];
            hex_buf[i * 2 + 1] = "0123456789abcdef"[b & 0x0f];
        }
        var out_buf: [72]u8 = undefined;
        const formatted = std.fmt.bufPrint(&out_buf, "{s}_{d}", .{
            hex_buf[0..64],
            outpoint.index,
        }) catch unreachable;
        try result.addEvent(ctx.allocator, formatted);
    } else if (ctx.outpoint) |_| {
        // Deploy — emit deploy + id events
        try result.addEvent(ctx.allocator, "deploy");
    }

    return result;
}

test "parse shrug returns null for non-shrug script" {
    const allocator = std.testing.allocator;
    var script = [_]u8{ 0x76, 0xa9, 20 } ++ [_]u8{0xaa} ** 20 ++ [_]u8{ 0x88, 0xac };

    var ctx = ParseContext.init(allocator, &script, 1, null);
    defer ctx.deinit();

    try std.testing.expect(try parse(&ctx) == null);
}
