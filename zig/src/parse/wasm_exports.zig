const std = @import("std");
const protobuf = @import("protobuf");
const main = @import("main.zig");
const pb = @import("parse_pb.zig");

var arena = std.heap.ArenaAllocator.init(std.heap.wasm_allocator);

/// Allocate memory for the host to write input data into.
export fn alloc(len: u32) u32 {
    const slice = arena.allocator().alloc(u8, len) catch return 0;
    return @intFromPtr(slice.ptr);
}

/// Free all arena memory after the host has read the results.
export fn dealloc() void {
    _ = arena.reset(.retain_capacity);
}

/// Parse a BEEF transaction. Host writes BEEF bytes at ptr/len,
/// calls this, then reads the result from the returned ptr.
/// Returns a pointer to [4 bytes: len][len bytes: protobuf-encoded BeefParseResult].
export fn parse_beef(ptr: u32, len: u32) u32 {
    const allocator = arena.allocator();
    const beef_bytes = @as([*]const u8, @ptrFromInt(ptr))[0..len];

    var result = main.parseBeefBytes(allocator, beef_bytes) catch return 0;
    defer result.deinit(allocator);

    return encodeResult(allocator, &result);
}

/// Parse a proto-encoded AtomicBeef. Same interface as parse_beef but input
/// is protobuf AtomicBeef instead of raw BEEF wire bytes.
export fn parse_atomic_beef(ptr: u32, len: u32) u32 {
    const allocator = arena.allocator();
    const proto_bytes = @as([*]const u8, @ptrFromInt(ptr))[0..len];

    var result = main.parseAtomicBeefProto(allocator, proto_bytes) catch return 0;
    defer result.deinit(allocator);

    return encodeResult(allocator, &result);
}

fn encodeResult(allocator: std.mem.Allocator, result: *const main.BeefParseResult) u32 {
    var pb_result = pb.BeefParseResult{
        .txid = if (result.txid) |t| &t.bytes else &.{},
        .block_height = result.block_height,
        .block_idx = result.block_idx,
    };

    for (result.outputs.items) |*output| {
        pb_result.outputs.append(allocator, toProtoOutput(output)) catch return 0;
    }
    for (result.spends.items) |*spend| {
        pb_result.spends.append(allocator, toProtoOutput(spend)) catch return 0;
    }

    var w: std.Io.Writer.Allocating = .init(allocator);
    pb_result.encode(&w.writer, allocator) catch return 0;
    w.writer.flush() catch return 0;
    const encoded = w.writer.buffer[0..w.writer.end];

    const out = allocator.alloc(u8, 4 + encoded.len) catch return 0;
    std.mem.writeInt(u32, out[0..4], @intCast(encoded.len), .little);
    @memcpy(out[4..], encoded);

    return @intFromPtr(out.ptr);
}

fn toProtoOutput(output: *const main.IndexedOutput) pb.IndexedOutput {
    var pb_out = pb.IndexedOutput{
        .outpoint = .{
            .txid = &output.outpoint.txid.bytes,
            .index = output.outpoint.index,
        },
        .satoshis = output.satoshis,
        .block_height = output.block_height,
        .block_idx = output.block_idx,
        .spend_txid = if (output.spend_txid) |t| &t.bytes else &.{},
    };

    pb_out.events = output.events;
    pb_out.owners = .{};
    for (output.owners.items) |owner| {
        pb_out.owners.append(output.ctx.allocator, &owner) catch {};
    }

    return pb_out;
}
