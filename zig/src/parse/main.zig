const std = @import("std");
const bsvz = @import("bsvz");
pub const context = @import("context.zig");
pub const beef_pb = @import("beef_pb.zig");
const protobuf = @import("protobuf");

const ParseContext = context.ParseContext;
const ParseResult = context.ParseResult;
const OutPoint = context.OutPoint;
const Hash256 = bsvz.crypto.Hash256;
const Opcode = bsvz.script.opcode.Opcode;
const Script = bsvz.script.Script;
const standard = bsvz.script.standard;
const Transaction = bsvz.transaction.Transaction;

fn toHash256(h: bsvz.primitives.chainhash.Hash) Hash256 {
    return .{ .bytes = h.bytes };
}

fn bytesToHash256(b: []const u8) Hash256 {
    if (b.len < 32) return .{ .bytes = .{0} ** 32 };
    var h: Hash256 = undefined;
    @memcpy(&h.bytes, b[0..32]);
    return h;
}

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
pub const bitcom = @import("bitcom.zig");
pub const opns = @import("opns.zig");
pub const ordlock = @import("ordlock.zig");
pub const shrug = @import("shrug.zig");
// pub const bsv21 = @import("bsv21.zig"); // depends on inscription + JSON parsing

/// Default parser execution order matching Go's DefaultTags.
const default_parsers = [_]context.ParserFn{
    parse1Sat,
    parseP2PKH,
    lock.parse,
    inscription.parse,
    // bsv21 — requires JSON parsing, deferred
    ordlock.parse,
    cosign.parse,
    opns.parse,
    shrug.parse,
    bitcom.parseBitcom,
    bitcom.parseB,
    bitcom.parseMAP,
    bitcom.parseAIP,
    bitcom.parseBAP,
    bitcom.parseSigma,
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

/// Result of parsing a full BEEF transaction.
pub const BeefParseResult = struct {
    outputs: std.ArrayListUnmanaged(IndexedOutput),
    spends: std.ArrayListUnmanaged(IndexedOutput),
    txid: ?Hash256,
    block_height: u32,
    block_idx: u64,

    pub fn deinit(self: *BeefParseResult, allocator: std.mem.Allocator) void {
        for (self.outputs.items) |*o| o.deinit(allocator);
        self.outputs.deinit(allocator);
        for (self.spends.items) |*o| o.deinit(allocator);
        self.spends.deinit(allocator);
    }
};

/// A parsed output with its events and owners collected from all parsers.
pub const IndexedOutput = struct {
    outpoint: OutPoint,
    satoshis: u64,
    block_height: u32,
    block_idx: u64,
    events: std.ArrayListUnmanaged([]const u8),
    owners: std.ArrayListUnmanaged([20]u8),
    spend_txid: ?Hash256,
    ctx: ParseContext,

    pub fn deinit(self: *IndexedOutput, _: std.mem.Allocator) void {
        // events and owners point into ctx's arena — ctx.deinit() handles cleanup
        self.events.deinit(self.ctx.allocator);
        self.owners.deinit(self.ctx.allocator);
        self.ctx.deinit();
    }
};

/// Parse a full BEEF. Deserializes, runs all parsers on each output
/// and each spent input, returns IndexedOutputs.
/// Replaces Go's IndexContext.ParseTxn().
pub fn parseBeefBytes(allocator: std.mem.Allocator, beef_bytes: []const u8) !BeefParseResult {
    var parsed = try bsvz.transaction.beef.parseBeef(allocator, beef_bytes);
    defer parsed.deinit();

    const tx = parsed.tx orelse return error.NoTargetTransaction;
    const txid = parsed.txid orelse return error.NoTargetTxid;

    const txid256 = toHash256(txid);

    var block_height: u32 = 0;
    var block_idx: u64 = 0;
    if (tx.merkle_path) |mp| {
        block_height = mp.block_height;
        if (mp.path.len > 0) {
            for (mp.path[0]) |leaf| {
                if (leaf.hash) |h| {
                    if (std.mem.eql(u8, &h.bytes, &txid.bytes)) {
                        block_idx = leaf.offset;
                        break;
                    }
                }
            }
        }
    }

    var result = BeefParseResult{
        .outputs = .{},
        .spends = .{},
        .txid = txid256,
        .block_height = block_height,
        .block_idx = block_idx,
    };
    errdefer result.deinit(allocator);

    // Parse outputs
    for (tx.outputs, 0..) |output, vout| {
        const outpoint = OutPoint{
            .txid = txid256,
            .index = @intCast(vout),
        };
        const sats: u64 = @intCast(@max(0, output.satoshis));
        var parse_ctx = try parseOutput(allocator, output.locking_script.bytes, sats, outpoint);

        var events = std.ArrayListUnmanaged([]const u8){};
        var owners = std.ArrayListUnmanaged([20]u8){};
        var it = parse_ctx.results.iterator();
        while (it.next()) |entry| {
            for (entry.value_ptr.events.items) |event| {
                try events.append(allocator, event);
            }
            for (entry.value_ptr.owners.items) |owner| {
                try owners.append(allocator, owner);
            }
        }

        try result.outputs.append(allocator, .{
            .outpoint = outpoint,
            .satoshis = sats,
            .block_height = block_height,
            .block_idx = block_idx,
            .events = events,
            .owners = owners,
            .spend_txid = null,
            .ctx = parse_ctx,
        });
    }

    // Parse spends (inputs)
    for (tx.inputs) |input| {
        const source_output = input.source_output orelse {
            try result.spends.append(allocator, .{
                .outpoint = input.previous_outpoint,
                .satoshis = 0,
                .block_height = 0,
                .block_idx = 0,
                .events = .{},
                .owners = .{},
                .spend_txid = txid256,
                .ctx = ParseContext.init(allocator, &.{}, 0, null),
            });
            continue;
        };

        const sats: u64 = @intCast(@max(0, source_output.satoshis));
        var parse_ctx = try parseOutput(allocator, source_output.locking_script.bytes, sats, input.previous_outpoint);

        var events = std.ArrayListUnmanaged([]const u8){};
        var owners = std.ArrayListUnmanaged([20]u8){};
        var it = parse_ctx.results.iterator();
        while (it.next()) |entry| {
            for (entry.value_ptr.events.items) |event| {
                try events.append(allocator, event);
            }
            for (entry.value_ptr.owners.items) |owner| {
                try owners.append(allocator, owner);
            }
        }

        try result.spends.append(allocator, .{
            .outpoint = input.previous_outpoint,
            .satoshis = sats,
            .block_height = 0,
            .block_idx = 0,
            .events = events,
            .owners = owners,
            .spend_txid = txid256,
            .ctx = parse_ctx,
        });
    }

    return result;
}

/// Parse from a proto-encoded AtomicBeef. Txids are pre-computed in the proto,
/// so no hashing is needed. Deserializes raw_tx bytes for the target and its
/// input sources, runs all parsers.
pub fn parseAtomicBeefProto(allocator: std.mem.Allocator, proto_bytes: []const u8) !BeefParseResult {
    var reader: std.Io.Reader = .fixed(proto_bytes);
    var atomic = try beef_pb.AtomicBeef.decode(&reader, allocator);
    defer atomic.deinit(allocator);

    if (atomic.txid.len < 32) return error.NoTargetTxid;
    const target_txid = bytesToHash256(atomic.txid);

    const beef = atomic.beef orelse return error.NoBeef;

    // Find target BeefTx
    var target_raw_tx: ?[]const u8 = null;
    var target_bump_index: ?u32 = null;
    for (beef.transactions.items) |btx| {
        if (btx.txid.len >= 32 and std.mem.eql(u8, btx.txid[0..32], &target_txid.bytes)) {
            target_raw_tx = btx.raw_tx;
            target_bump_index = btx.bump_index;
            break;
        }
    }
    const raw_tx = target_raw_tx orelse return error.NoTargetTransaction;

    // Deserialize target transaction
    var tx = try Transaction.parse(allocator, raw_tx);
    defer tx.deinit(allocator);

    // Get block height/index from bumps
    var block_height: u32 = 0;
    var block_idx: u64 = 0;
    if (target_bump_index) |bi| {
        if (bi < beef.bumps.items.len) {
            const bump = beef.bumps.items[bi];
            block_height = bump.block_height;
            for (bump.path.items) |level| {
                if (level.elements.items.len == 0) continue;
                for (level.elements.items) |elem| {
                    if (elem.hash.len >= 32 and std.mem.eql(u8, elem.hash[0..32], &target_txid.bytes)) {
                        block_idx = elem.offset;
                        break;
                    }
                }
                break; // only check first level (leaves)
            }
        }
    }

    var result = BeefParseResult{
        .outputs = .{},
        .spends = .{},
        .txid = target_txid,
        .block_height = block_height,
        .block_idx = block_idx,
    };
    errdefer result.deinit(allocator);

    // Parse outputs
    for (tx.outputs, 0..) |output, vout| {
        const outpoint = OutPoint{
            .txid = target_txid,
            .index = @intCast(vout),
        };
        const sats: u64 = @intCast(@max(0, output.satoshis));
        var parse_ctx = try parseOutput(allocator, output.locking_script.bytes, sats, outpoint);

        var events = std.ArrayListUnmanaged([]const u8){};
        var owners = std.ArrayListUnmanaged([20]u8){};
        var it = parse_ctx.results.iterator();
        while (it.next()) |entry| {
            for (entry.value_ptr.events.items) |event| {
                try events.append(allocator, event);
            }
            for (entry.value_ptr.owners.items) |owner| {
                try owners.append(allocator, owner);
            }
        }

        try result.outputs.append(allocator, .{
            .outpoint = outpoint,
            .satoshis = sats,
            .block_height = block_height,
            .block_idx = block_idx,
            .events = events,
            .owners = owners,
            .spend_txid = null,
            .ctx = parse_ctx,
        });
    }

    // Parse spends — find source transactions in the beef
    for (tx.inputs) |input| {
        const prev_txid_bytes = &input.previous_outpoint.txid.bytes;

        // Find source tx in beef
        var source_raw: ?[]const u8 = null;
        for (beef.transactions.items) |btx| {
            if (btx.txid.len >= 32 and std.mem.eql(u8, btx.txid[0..32], prev_txid_bytes)) {
                source_raw = btx.raw_tx;
                break;
            }
        }

        if (source_raw == null or source_raw.?.len == 0) {
            try result.spends.append(allocator, .{
                .outpoint = input.previous_outpoint,
                .satoshis = 0,
                .block_height = 0,
                .block_idx = 0,
                .events = .{},
                .owners = .{},
                .spend_txid = target_txid,
                .ctx = ParseContext.init(allocator, &.{}, 0, null),
            });
            continue;
        }

        var source_tx = Transaction.parse(allocator, source_raw.?) catch {
            try result.spends.append(allocator, .{
                .outpoint = input.previous_outpoint,
                .satoshis = 0,
                .block_height = 0,
                .block_idx = 0,
                .events = .{},
                .owners = .{},
                .spend_txid = target_txid,
                .ctx = ParseContext.init(allocator, &.{}, 0, null),
            });
            continue;
        };
        defer source_tx.deinit(allocator);

        const vout = input.previous_outpoint.index;
        if (vout >= source_tx.outputs.len) {
            try result.spends.append(allocator, .{
                .outpoint = input.previous_outpoint,
                .satoshis = 0,
                .block_height = 0,
                .block_idx = 0,
                .events = .{},
                .owners = .{},
                .spend_txid = target_txid,
                .ctx = ParseContext.init(allocator, &.{}, 0, null),
            });
            continue;
        }

        const source_output = source_tx.outputs[vout];
        const sats: u64 = @intCast(@max(0, source_output.satoshis));
        var parse_ctx = try parseOutput(allocator, source_output.locking_script.bytes, sats, input.previous_outpoint);

        var events = std.ArrayListUnmanaged([]const u8){};
        var owners = std.ArrayListUnmanaged([20]u8){};
        var it = parse_ctx.results.iterator();
        while (it.next()) |entry| {
            for (entry.value_ptr.events.items) |event| {
                try events.append(allocator, event);
            }
            for (entry.value_ptr.owners.items) |owner| {
                try owners.append(allocator, owner);
            }
        }

        try result.spends.append(allocator, .{
            .outpoint = input.previous_outpoint,
            .satoshis = sats,
            .block_height = 0,
            .block_idx = 0,
            .events = events,
            .owners = owners,
            .spend_txid = target_txid,
            .ctx = parse_ctx,
        });
    }

    return result;
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
