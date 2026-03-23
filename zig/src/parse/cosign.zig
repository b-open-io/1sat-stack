const std = @import("std");
const bsvz = @import("bsvz");
const ctx_mod = @import("context.zig");

const ParseContext = ctx_mod.ParseContext;
const ParseResult = ctx_mod.ParseResult;
const ScriptIterator = bsvz.script.parser.ScriptIterator;
const ScriptChunk = bsvz.script.chunk.ScriptChunk;
const Opcode = bsvz.script.opcode.Opcode;

fn isOpcode(chunk: ScriptChunk, op: Opcode) bool {
    return switch (chunk) {
        .opcode => |o| o == op,
        else => false,
    };
}

fn isPushLen(chunk: ScriptChunk, len: usize) bool {
    return switch (chunk) {
        .push_data => |pd| pd.data.len == len,
        else => false,
    };
}

pub const CosignData = struct {
    pkhash: [20]u8,
    cosigner: [33]u8,
};

/// Matches Go cosign.Decode: parse into chunks, slide a 7-chunk window looking for
/// OP_DUP OP_HASH160 PUSH(20) OP_EQUALVERIFY OP_CHECKSIGVERIFY PUSH(33) OP_CHECKSIG
pub fn parse(ctx: *ParseContext) anyerror!?ParseResult {
    var iter = ScriptIterator.initBytes(ctx.locking_script);

    var chunks: [128]ScriptChunk = undefined;
    var count: usize = 0;
    while (try iter.next()) |c| {
        if (count >= chunks.len) break;
        chunks[count] = c;
        count += 1;
    }

    if (count < 7) return null;

    for (0..count - 6) |i| {
        if (!isOpcode(chunks[i], .OP_DUP)) continue;
        if (!isOpcode(chunks[i + 1], .OP_HASH160)) continue;
        if (!isPushLen(chunks[i + 2], 20)) continue;
        if (!isOpcode(chunks[i + 3], .OP_EQUALVERIFY)) continue;
        if (!isOpcode(chunks[i + 4], .OP_CHECKSIGVERIFY)) continue;
        if (!isPushLen(chunks[i + 5], 33)) continue;
        if (!isOpcode(chunks[i + 6], .OP_CHECKSIG)) continue;

        const c = chunks[i .. i + 7];

        const data = try ctx.allocator.create(CosignData);
        @memcpy(&data.pkhash, c[2].push_data.data[0..20]);
        @memcpy(&data.cosigner, c[5].push_data.data[0..33]);

        var result = ParseResult.init("cosign");
        result.data = data;
        try result.addOwner(ctx.allocator, data.pkhash);
        return result;
    }

    return null;
}

test "parse cosign script" {
    const allocator = std.testing.allocator;

    // Build: OP_DUP OP_HASH160 PUSH(20) OP_EQUALVERIFY OP_CHECKSIGVERIFY PUSH(33) OP_CHECKSIG
    var pkhash: [20]u8 = undefined;
    for (&pkhash, 0..) |*b, i| b.* = @intCast(i);
    var cosigner: [33]u8 = undefined;
    cosigner[0] = 0x02;
    for (cosigner[1..], 0..) |*b, i| b.* = @intCast(i + 100);

    var script: [60]u8 = undefined;
    script[0] = @intFromEnum(Opcode.OP_DUP);
    script[1] = @intFromEnum(Opcode.OP_HASH160);
    script[2] = 20;
    @memcpy(script[3..23], &pkhash);
    script[23] = @intFromEnum(Opcode.OP_EQUALVERIFY);
    script[24] = @intFromEnum(Opcode.OP_CHECKSIGVERIFY);
    script[25] = 33;
    @memcpy(script[26..59], &cosigner);
    script[59] = @intFromEnum(Opcode.OP_CHECKSIG);

    var ctx = ParseContext.init(allocator, &script, 1, null);
    defer ctx.deinit();

    var result = (try parse(&ctx)).?;
    defer {
        if (result.data) |d| allocator.destroy(@as(*CosignData, @ptrCast(@alignCast(d))));
        result.deinit(allocator);
    }
    try std.testing.expectEqualStrings("cosign", result.tag);
    try std.testing.expectEqual(@as(usize, 1), result.owners.items.len);
    try std.testing.expectEqualSlices(u8, &pkhash, &result.owners.items[0]);

    const data: *CosignData = @ptrCast(@alignCast(result.data.?));
    try std.testing.expectEqualSlices(u8, &cosigner, &data.cosigner);
}

test "parse cosign returns null for p2pkh" {
    const allocator = std.testing.allocator;
    var script = [_]u8{ 0x76, 0xa9, 20 } ++ [_]u8{0xaa} ** 20 ++ [_]u8{ 0x88, 0xac };

    var ctx = ParseContext.init(allocator, &script, 1, null);
    defer ctx.deinit();

    try std.testing.expect(try parse(&ctx) == null);
}
