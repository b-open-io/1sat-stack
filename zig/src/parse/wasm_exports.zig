const std = @import("std");
const main = @import("main.zig");

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
/// Returns a pointer to a length-prefixed result buffer.
export fn parse_beef(ptr: u32, len: u32) u32 {
    const allocator = arena.allocator();
    const beef_bytes = @as([*]const u8, @ptrFromInt(ptr))[0..len];

    var result = main.parseBeefBytes(allocator, beef_bytes) catch return 0;
    defer result.deinit(allocator);

    var buf = std.ArrayListUnmanaged(u8){};

    // Output count + outputs
    writeU32(&buf, allocator, @intCast(result.outputs.items.len));
    for (result.outputs.items) |*output| {
        serializeIndexedOutput(&buf, allocator, output);
    }

    // Spend count + spends
    writeU32(&buf, allocator, @intCast(result.spends.items.len));
    for (result.spends.items) |*spend| {
        serializeIndexedOutput(&buf, allocator, spend);
    }

    // txid
    if (result.txid) |txid| {
        buf.appendSlice(allocator, &txid.bytes) catch return 0;
    } else {
        buf.appendNTimes(allocator, 0, 32) catch return 0;
    }

    // block height + idx
    writeU32(&buf, allocator, result.block_height);
    writeU64(&buf, allocator, result.block_idx);

    // Length-prefix the whole thing
    const total_len = buf.items.len;
    const out = allocator.alloc(u8, 4 + total_len) catch return 0;
    std.mem.writeInt(u32, out[0..4], @intCast(total_len), .little);
    @memcpy(out[4..], buf.items);

    return @intFromPtr(out.ptr);
}

fn serializeIndexedOutput(buf: *std.ArrayListUnmanaged(u8), allocator: std.mem.Allocator, output: *const main.IndexedOutput) void {
    buf.appendSlice(allocator, &output.outpoint.txid.bytes) catch return;
    var vout_bytes: [4]u8 = undefined;
    std.mem.writeInt(u32, &vout_bytes, output.outpoint.index, .little);
    buf.appendSlice(allocator, &vout_bytes) catch return;

    var sat_bytes: [8]u8 = undefined;
    std.mem.writeInt(u64, &sat_bytes, output.satoshis, .little);
    buf.appendSlice(allocator, &sat_bytes) catch return;

    writeU32(buf, allocator, @intCast(output.events.items.len));
    for (output.events.items) |event| {
        writeU32(buf, allocator, @intCast(event.len));
        buf.appendSlice(allocator, event) catch return;
    }

    writeU32(buf, allocator, @intCast(output.owners.items.len));
    for (output.owners.items) |owner| {
        buf.appendSlice(allocator, &owner) catch return;
    }

    if (output.spend_txid) |txid| {
        buf.append(allocator, 1) catch return;
        buf.appendSlice(allocator, &txid.bytes) catch return;
    } else {
        buf.append(allocator, 0) catch return;
    }
}

fn writeU32(buf: *std.ArrayListUnmanaged(u8), allocator: std.mem.Allocator, val: u32) void {
    var bytes: [4]u8 = undefined;
    std.mem.writeInt(u32, &bytes, val, .little);
    buf.appendSlice(allocator, &bytes) catch {};
}

fn writeU64(buf: *std.ArrayListUnmanaged(u8), allocator: std.mem.Allocator, val: u64) void {
    var bytes: [8]u8 = undefined;
    std.mem.writeInt(u64, &bytes, val, .little);
    buf.appendSlice(allocator, &bytes) catch {};
}
