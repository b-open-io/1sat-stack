const std = @import("std");
const bsvz = @import("bsvz");
const ctx_mod = @import("context.zig");
const lock_mod = @import("lock.zig");

const ParseContext = ctx_mod.ParseContext;
const ParseResult = ctx_mod.ParseResult;
const ScriptIterator = bsvz.script.parser.ScriptIterator;

pub const OrdLockData = struct {
    seller_pkhash: [20]u8,
    payout: []const u8,
};

// Full OrdLockPrefix from go-templates/template/ordlock/constants.go
const ordlock_prefix = lock_mod.hexDecode("2097dfd76851bf465e8f715593b217714858bbe9570ff3bd5e33840a34e20ff0262102ba79df5f8ae7604a9830f03c7933028186aede0675a16f025dc4f8be8eec0382201008ce7480da41702918d1ec8e6849ba32b4d65b1e40dc669c31a1e6306b266c0000");

// Full OrdLockSuffix from go-templates/template/ordlock/constants.go
const ordlock_suffix = lock_mod.hexDecode("615179547a75537a537a537a0079537a75527a527a7575615579008763567901c161517957795779210ac407f0e4bd44bfc207355a778b046225a7068fc59ee7eda43ad905aadbffc800206c266b30e6a1319c66dc401e5bd6b432ba49688eecd118297041da8074ce081059795679615679aa0079610079517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e01007e81517a75615779567956795679567961537956795479577995939521414136d08c5ed2bf3ba048afe6dcaebafeffffffffffffffffffffffffffffff00517951796151795179970079009f63007952799367007968517a75517a75517a7561527a75517a517951795296a0630079527994527a75517a6853798277527982775379012080517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f517f7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e7c7e01205279947f7754537993527993013051797e527e54797e58797e527e53797e52797e57797e0079517a75517a75517a75517a75517a75517a75517a75517a75517a75517a75517a75517a75517a756100795779ac517a75517a75517a75517a75517a75517a75517a75517a75517a7561517a75517a756169587951797e58797eaa577961007982775179517958947f7551790128947f77517a75517a75618777777777777777777767557951876351795779a9876957795779ac777777777777777767006868");

/// Matches Go ordlock.Decode: find OrdLockPrefix + OrdLockSuffix by bytes.Index,
/// then DecodeScript the data between them to extract seller PKHash and payout.
pub fn parse(ctx: *ParseContext) anyerror!?ParseResult {
    const script = ctx.locking_script;

    const prefix_pos = std.mem.indexOf(u8, script, &ordlock_prefix) orelse return null;
    if (std.mem.indexOf(u8, script, &ordlock_suffix) == null) return null;

    // Data between prefix and suffix
    const data_start = prefix_pos + ordlock_prefix.len;
    const suffix_pos = std.mem.indexOf(u8, script[data_start..], &ordlock_suffix) orelse return null;
    const data_section = script[data_start .. data_start + suffix_pos];

    // Parse the data section with ScriptIterator
    var iter = ScriptIterator.initBytes(data_section);

    // First push: seller PKHash (20 bytes)
    const seller_chunk = try iter.next() orelse return null;
    const seller_data = switch (seller_chunk) {
        .push_data => |pd| pd.data,
        else => return null,
    };
    if (seller_data.len != 20) return null;

    // Second push: payout (serialized TransactionOutput)
    const payout_chunk = try iter.next() orelse return null;
    const payout = switch (payout_chunk) {
        .push_data => |pd| pd.data,
        else => return null,
    };

    const data = try ctx.allocator.create(OrdLockData);
    data.* = .{
        .seller_pkhash = seller_data[0..20].*,
        .payout = payout,
    };

    var result = ParseResult.init("ordlock");
    result.data = data;
    try result.addOwner(ctx.allocator, data.seller_pkhash);
    return result;
}

test "parse ordlock returns null for non-ordlock script" {
    const allocator = std.testing.allocator;
    var script = [_]u8{ 0x76, 0xa9, 20 } ++ [_]u8{0xaa} ** 20 ++ [_]u8{ 0x88, 0xac };

    var ctx = ParseContext.init(allocator, &script, 1, null);
    defer ctx.deinit();

    try std.testing.expect(try parse(&ctx) == null);
}
