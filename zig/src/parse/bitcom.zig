const std = @import("std");
const bsvz = @import("bsvz");
const ctx_mod = @import("context.zig");

const ParseContext = ctx_mod.ParseContext;
const ParseResult = ctx_mod.ParseResult;
const ScriptIterator = bsvz.script.parser.ScriptIterator;
const ScriptChunk = bsvz.script.chunk.ScriptChunk;
const Opcode = bsvz.script.opcode.Opcode;

// Bitcom protocol prefixes
const b_prefix = "19HxigV4QyBv3tHpQVcUEQyq1pzZVdoAut";
const map_prefix = "1PuQa7K62MiKCtssSLKy1kh56WWU7MtUR5";
const aip_prefix = "15PciHG22SNLQJXMoSUaWVi7WSqc7hCfva";
const bap_prefix = "1BAPSuaPnfGnSBM3GLV9yhxUdYe4vGbdMT";
const sigma_prefix = "SIGMA";

pub const BitcomProtocol = struct {
    protocol: []const u8,
    script: []const u8,
    pos: usize,
};

pub const Bitcom = struct {
    protocols: []BitcomProtocol,
    script_prefix: []const u8,
};

// B protocol data
pub const BData = struct {
    media_type: []const u8,
    encoding: []const u8,
    data: []const u8,
    filename: []const u8,
};

// MAP protocol data
pub const MapData = struct {
    cmd: []const u8,
    keys: std.ArrayListUnmanaged([]const u8),
    values: std.ArrayListUnmanaged([]const u8),
};

/// Base bitcom parser. Matches Go bitcom.Decode:
/// Find OP_RETURN (via ScriptIterator which yields op_return_data),
/// then split the OP_RETURN tail by pipe separators, reading the
/// protocol prefix for each section.
pub fn parseBitcom(ctx: *ParseContext) anyerror!?ParseResult {
    const script = ctx.locking_script;

    // Find OP_RETURN position using iterator
    var iter = ScriptIterator.initBytes(script);
    var prefix_end: usize = 0;

    while (try iter.next()) |chunk| {
        switch (chunk) {
            .op_return_data => |_| {
                // iter.pos() is now past the OP_RETURN and its tail
                // prefix_end is the byte before OP_RETURN
                // The OP_RETURN byte is at iter.pos() - tail.len - 1
                break;
            },
            else => {
                prefix_end = iter.pos();
            },
        }
    } else {
        return null; // No OP_RETURN found
    }

    // The OP_RETURN byte is at prefix_end in the script
    // Check if it's OP_FALSE OP_RETURN (2 bytes) or just OP_RETURN (1 byte)
    var op_return_pos = prefix_end;
    if (op_return_pos > 0 and script[op_return_pos - 1] == @intFromEnum(Opcode.OP_0)) {
        op_return_pos -= 1;
    }

    const script_prefix = if (op_return_pos > 0) script[0 .. op_return_pos] else &[_]u8{};
    const tail_start = prefix_end + 1; // skip the OP_RETURN byte

    if (tail_start >= script.len) return null;

    // Parse the tail: split by pipe "|" separators
    // Each section starts with a protocol prefix push, followed by protocol-specific data
    var protocols = std.ArrayListUnmanaged(BitcomProtocol){};

    var section_start = tail_start;
    while (section_start < script.len) {
        // Find next pipe separator
        var pipe_pos: ?usize = null;
        const scan_pos = section_start;
        var scan_iter = ScriptIterator.initBytes(script[scan_pos..]);
        var first_push: ?[]const u8 = null;
        var first_push_end: usize = 0;

        while (try scan_iter.next()) |chunk| {
            switch (chunk) {
                .push_data => |pd| {
                    if (first_push == null) {
                        first_push = pd.data;
                        first_push_end = scan_pos + scan_iter.pos();
                    }
                    if (pd.data.len == 1 and pd.data[0] == '|') {
                        pipe_pos = scan_pos + scan_iter.pos() - 2; // position of the pipe push opcode
                        break;
                    }
                },
                .opcode => {
                    if (first_push == null) {
                        // Opcode before any push — not a valid protocol section
                        break;
                    }
                },
                .op_return_data => break,
            }
        }

        const proto_prefix = first_push orelse break;
        const section_end = pipe_pos orelse script.len;
        const proto_script = script[first_push_end..section_end];

        try protocols.append(ctx.allocator, .{
            .protocol = proto_prefix,
            .script = proto_script,
            .pos = section_start,
        });

        if (pipe_pos) |pp| {
            section_start = pp + 2; // skip pipe push (opcode + '|')
        } else {
            break;
        }
    }

    if (protocols.items.len == 0) return null;

    const data = try ctx.allocator.create(Bitcom);
    data.* = .{
        .protocols = protocols.items,
        .script_prefix = script_prefix,
    };

    var result = ParseResult.init("bitcom");
    result.data = data;
    return result;
}

/// B protocol parser. Reads DATA, MEDIA_TYPE, ENCODING, optional FILENAME
/// from the B protocol section in a bitcom result.
pub fn parseB(ctx: *ParseContext) anyerror!?ParseResult {
    const bitcom = getBitcom(ctx) orelse return null;

    for (bitcom.protocols) |proto| {
        if (!std.mem.eql(u8, proto.protocol, b_prefix)) continue;

        var iter = ScriptIterator.initBytes(proto.script);

        // DATA
        const data_chunk = try iter.next() orelse return null;
        const file_data = switch (data_chunk) {
            .push_data => |pd| pd.data,
            else => return null,
        };

        // MEDIA_TYPE
        const type_chunk = try iter.next() orelse return null;
        const media_type = switch (type_chunk) {
            .push_data => |pd| pd.data,
            else => return null,
        };

        // ENCODING
        const enc_chunk = try iter.next() orelse return null;
        const encoding = switch (enc_chunk) {
            .push_data => |pd| pd.data,
            else => &[_]u8{},
        };

        // optional FILENAME
        var filename: []const u8 = &.{};
        if (try iter.next()) |fn_chunk| {
            if (fn_chunk == .push_data) {
                filename = fn_chunk.push_data.data;
            }
        }

        const b = try ctx.allocator.create(BData);
        b.* = .{
            .media_type = media_type,
            .encoding = encoding,
            .data = file_data,
            .filename = filename,
        };

        var result = ParseResult.init("b");
        result.data = b;
        // Event: type:{mediaType}
        if (media_type.len > 0) {
            try result.addEvent(ctx.allocator, media_type);
        }
        return result;
    }
    return null;
}

/// MAP protocol parser. Reads CMD then key-value pairs for SET.
pub fn parseMAP(ctx: *ParseContext) anyerror!?ParseResult {
    const bitcom = getBitcom(ctx) orelse return null;

    for (bitcom.protocols) |proto| {
        if (!std.mem.eql(u8, proto.protocol, map_prefix)) continue;

        var iter = ScriptIterator.initBytes(proto.script);

        // CMD
        const cmd_chunk = try iter.next() orelse return null;
        const cmd = switch (cmd_chunk) {
            .push_data => |pd| pd.data,
            else => return null,
        };

        var map_data = MapData{
            .cmd = cmd,
            .keys = .{},
            .values = .{},
        };

        // For SET: read key-value pairs
        if (std.mem.eql(u8, cmd, "SET")) {
            while (true) {
                const key_chunk = try iter.next() orelse break;
                const key = switch (key_chunk) {
                    .push_data => |pd| pd.data,
                    else => break,
                };
                const val_chunk = try iter.next() orelse break;
                const val = switch (val_chunk) {
                    .push_data => |pd| pd.data,
                    else => break,
                };
                try map_data.keys.append(ctx.allocator, key);
                try map_data.values.append(ctx.allocator, val);
            }
        }

        const data = try ctx.allocator.create(MapData);
        data.* = map_data;

        var result = ParseResult.init("map");
        result.data = data;

        // Event: map:type:{value} when a "type" key exists
        for (map_data.keys.items, 0..) |key, i| {
            if (std.mem.eql(u8, key, "type") and i < map_data.values.items.len) {
                try result.addEvent(ctx.allocator, map_data.values.items[i]);
            }
        }

        return result;
    }
    return null;
}

/// AIP protocol parser. Reads ALGORITHM, ADDRESS, SIGNATURE.
pub fn parseAIP(ctx: *ParseContext) anyerror!?ParseResult {
    const bitcom = getBitcom(ctx) orelse return null;

    for (bitcom.protocols) |proto| {
        if (!std.mem.eql(u8, proto.protocol, aip_prefix)) continue;

        var iter = ScriptIterator.initBytes(proto.script);

        const alg_chunk = try iter.next() orelse continue;
        const addr_chunk = try iter.next() orelse continue;
        const sig_chunk = try iter.next() orelse continue;

        const address = switch (addr_chunk) {
            .push_data => |pd| pd.data,
            else => continue,
        };
        _ = alg_chunk;
        _ = sig_chunk;

        var result = ParseResult.init("aip");
        if (address.len > 0) {
            try result.addEvent(ctx.allocator, address);
        }
        return result;
    }
    return null;
}

/// BAP protocol parser. Reads TYPE then type-specific fields.
pub fn parseBAP(ctx: *ParseContext) anyerror!?ParseResult {
    const bitcom = getBitcom(ctx) orelse return null;

    for (bitcom.protocols) |proto| {
        if (!std.mem.eql(u8, proto.protocol, bap_prefix)) continue;

        var iter = ScriptIterator.initBytes(proto.script);

        const type_chunk = try iter.next() orelse continue;
        const bap_type = switch (type_chunk) {
            .push_data => |pd| pd.data,
            else => continue,
        };

        var result = ParseResult.init("bap");
        if (bap_type.len > 0) {
            try result.addEvent(ctx.allocator, bap_type);
        }
        return result;
    }
    return null;
}

/// SIGMA protocol parser. Reads ALGORITHM, SIGNER_ADDRESS, SIGNATURE_VALUE.
pub fn parseSigma(ctx: *ParseContext) anyerror!?ParseResult {
    const bitcom = getBitcom(ctx) orelse return null;

    for (bitcom.protocols) |proto| {
        if (!std.mem.eql(u8, proto.protocol, sigma_prefix)) continue;

        var iter = ScriptIterator.initBytes(proto.script);

        const alg_chunk = try iter.next() orelse continue;
        const addr_chunk = try iter.next() orelse continue;
        _ = alg_chunk;

        const address = switch (addr_chunk) {
            .push_data => |pd| pd.data,
            else => continue,
        };

        var result = ParseResult.init("sigma");
        if (address.len > 0) {
            try result.addEvent(ctx.allocator, address);
        }
        return result;
    }
    return null;
}

fn getBitcom(ctx: *const ParseContext) ?*const Bitcom {
    const r = ctx.getResult("bitcom") orelse return null;
    return @ptrCast(@alignCast(r.data orelse return null));
}

// --- Tests ---

fn pushData(buf: []u8, pos: *usize, data: []const u8) void {
    buf[pos.*] = @intCast(data.len);
    pos.* += 1;
    @memcpy(buf[pos.*..][0..data.len], data);
    pos.* += data.len;
}

test "parse bitcom with MAP SET" {
    // Use arena allocator — mirrors WASM production use where arena handles cleanup
    var arena = std.heap.ArenaAllocator.init(std.testing.allocator);
    defer arena.deinit();
    const allocator = arena.allocator();

    // Build: OP_RETURN PUSH(map_prefix) PUSH("SET") PUSH("type") PUSH("post")
    var script_buf: [256]u8 = undefined;
    var pos: usize = 0;
    script_buf[pos] = @intFromEnum(Opcode.OP_RETURN);
    pos += 1;
    pushData(&script_buf, &pos, map_prefix);
    pushData(&script_buf, &pos, "SET");
    pushData(&script_buf, &pos, "type");
    pushData(&script_buf, &pos, "post");
    const script = script_buf[0..pos];

    var ctx = ParseContext.init(allocator, script, 1, null);

    if (try parseBitcom(&ctx)) |result| {
        try ctx.addResult(result);
    }

    const bc_result = ctx.getResult("bitcom");
    try std.testing.expect(bc_result != null);

    const bc: *Bitcom = @ptrCast(@alignCast(bc_result.?.data.?));
    try std.testing.expectEqual(@as(usize, 1), bc.protocols.len);
    try std.testing.expectEqualStrings(map_prefix, bc.protocols[0].protocol);

    // Run MAP parser
    const map_result = (try parseMAP(&ctx)).?;
    try std.testing.expectEqualStrings("map", map_result.tag);
    try std.testing.expectEqual(@as(usize, 1), map_result.events.items.len);
    try std.testing.expectEqualStrings("post", map_result.events.items[0]);
}
