const std = @import("std");
const bsvz = @import("bsvz");
const ctx_mod = @import("context.zig");

const ParseContext = ctx_mod.ParseContext;
const ParseResult = ctx_mod.ParseResult;
const ScriptIterator = bsvz.script.parser.ScriptIterator;

pub const LockData = struct {
    pkhash: [20]u8,
    until: u32,
};

fn hexDecode(comptime hex: []const u8) [hex.len / 2]u8 {
    @setEvalBranchQuota(hex.len * 10);
    var buf: [hex.len / 2]u8 = undefined;
    for (0..hex.len / 2) |i| {
        buf[i] = (hexVal(hex[i * 2]) << 4) | hexVal(hex[i * 2 + 1]);
    }
    return buf;
}

fn hexVal(c: u8) u8 {
    return switch (c) {
        '0'...'9' => c - '0',
        'a'...'f' => c - 'a' + 10,
        'A'...'F' => c - 'A' + 10,
        else => unreachable,
    };
}

// Full LockPrefix from go-templates/template/lockup/script.go
const lock_prefix = hexDecode("2097dfd76851bf465e8f715593b217714858bbe9570ff3bd5e33840a34e20ff0262102ba79df5f8ae7604a9830f03c7933028186aede0675a16f025dc4f8be8eec0382201008ce7480da41702918d1ec8e6849ba32b4d65b1e40dc669c31a1e6306b266c0000");

// Full LockSuffix from go-templates/template/lockup/script.go
const lock_suffix = hexDecode("610079040065cd1d9f690079547a75537a537a537a5179537a75527a527a7575615579014161517957795779210ac407f0e4bd44bfc207355a778b046225a7068fc59ee7eda43ad905aadbffc800206c266b30e6a1319c66dc401e5bd6b432ba49688eecd118297041da8074ce081059795679615679aa0079610079517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e01007e81517a75615779567956795679567961537956795479577995939521414136d08c5ed2bf3ba048afe6dcaebafeffffffffffffffffffffffffffffff00517951796151795179970079009f63007952799367007968517a75517a75517a7561527a75517a517951795296a0630079527994527a75517a6853798277527982775379012080517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e01205279947f7754537993527993013051797e527e54797e58797e527e53797e52797e57797e0079517a75517a75517a75517a75517a75517a75517a75517a75517a75517a75517a75517a75517a756100795779ac517a75517a75517a75517a75517a75517a75517a75517a75517a7561517a75517a756169557961007961007982775179517954947f75517958947f77517a75517a756161007901007e81517a7561517a7561040065cd1d9f6955796100796100798277517951790128947f755179012c947f77517a75517a756161007901007e81517a7561517a756105ffffffff009f69557961007961007982775179517954947f75517958947f77517a75517a756161007901007e81517a7561517a75615279a2695679a95179876957795779ac7777777777777777");

/// Matches Go lockup.Decode: find full LockPrefix via bytes.Index,
/// verify full LockSuffix via bytes.Contains after prefix,
/// then ReadOp for 20-byte PKHash and variable-length nLockTime.
pub fn parse(ctx: *ParseContext) anyerror!?ParseResult {
    const script = ctx.locking_script;

    const prefix_pos = std.mem.indexOf(u8, script, &lock_prefix) orelse return null;

    // Verify full suffix exists after the prefix
    if (std.mem.indexOf(u8, script[prefix_pos..], &lock_suffix) == null) return null;

    // Use iterator starting after the prefix to read the two data pushes
    var iter = ScriptIterator.initBytes(script[prefix_pos + lock_prefix.len ..]);

    // Read PKHash: expect PUSH(20 bytes)
    const pkh_chunk = try iter.next() orelse return null;
    if (pkh_chunk != .push_data or pkh_chunk.push_data.data.len != 20) return null;

    // Read nLockTime: expect PUSH(1-5 bytes), little-endian uint32
    const until_chunk = try iter.next() orelse return null;
    if (until_chunk != .push_data or until_chunk.push_data.data.len == 0 or until_chunk.push_data.data.len > 5) return null;

    var until_bytes: [4]u8 = .{ 0, 0, 0, 0 };
    @memcpy(until_bytes[0..until_chunk.push_data.data.len], until_chunk.push_data.data);

    const data = try ctx.allocator.create(LockData);
    data.* = .{
        .pkhash = pkh_chunk.push_data.data[0..20].*,
        .until = std.mem.readInt(u32, &until_bytes, .little),
    };

    var result = ParseResult.init("lock");
    result.data = data;
    try result.addEvent(ctx.allocator, "lock");
    try result.addOwner(ctx.allocator, data.pkhash);
    return result;
}

test "parse lock script" {
    const allocator = std.testing.allocator;
    var pkhash: [20]u8 = undefined;
    for (&pkhash, 0..) |*b, i| b.* = @intCast(i);
    const until: u32 = 850000;

    // Build: lock_prefix + PUSH(20) pkhash + PUSH(4) until_le + lock_suffix
    var script: [lock_prefix.len + 1 + 20 + 1 + 4 + lock_suffix.len]u8 = undefined;
    var pos: usize = 0;
    @memcpy(script[pos..][0..lock_prefix.len], &lock_prefix);
    pos += lock_prefix.len;
    script[pos] = 20;
    pos += 1;
    @memcpy(script[pos..][0..20], &pkhash);
    pos += 20;
    script[pos] = 4;
    pos += 1;
    std.mem.writeInt(u32, script[pos..][0..4], until, .little);
    pos += 4;
    @memcpy(script[pos..][0..lock_suffix.len], &lock_suffix);

    var ctx = ParseContext.init(allocator, &script, 1, null);
    defer ctx.deinit();

    var result = (try parse(&ctx)).?;
    defer {
        if (result.data) |d| allocator.destroy(@as(*LockData, @ptrCast(@alignCast(d))));
        result.deinit(allocator);
    }
    try std.testing.expectEqualStrings("lock", result.tag);
    const data: *LockData = @ptrCast(@alignCast(result.data.?));
    try std.testing.expectEqual(until, data.until);
    try std.testing.expectEqualSlices(u8, &pkhash, &data.pkhash);
}

test "parse lock returns null for non-lock script" {
    const allocator = std.testing.allocator;
    var script = [_]u8{ 0x76, 0xa9, 20 } ++ [_]u8{0xaa} ** 20 ++ [_]u8{ 0x88, 0xac };

    var ctx = ParseContext.init(allocator, &script, 1, null);
    defer ctx.deinit();

    try std.testing.expect(try parse(&ctx) == null);
}
