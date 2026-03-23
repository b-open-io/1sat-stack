const std = @import("std");
const bsvz = @import("bsvz");
const ctx_mod = @import("context.zig");

const ParseContext = ctx_mod.ParseContext;
const ParseResult = ctx_mod.ParseResult;
const OutPoint = ctx_mod.OutPoint;
const ScriptIterator = bsvz.script.parser.ScriptIterator;
const ScriptChunk = bsvz.script.chunk.ScriptChunk;
const Opcode = bsvz.script.opcode.Opcode;
const Script = bsvz.script.Script;
const standard = bsvz.script.standard;

pub const Inscription = struct {
    content_type: []const u8,
    content: []const u8,
    parent: ?OutPoint,
    script_prefix: []const u8,
    script_suffix: []const u8,
};

/// Matches Go inscription.Decode: walk script with ReadOp, find OP_FALSE OP_IF
/// followed by a 3-byte "ord" push. Then read field-value pairs until field 0 (content).
/// After content, expect OP_ENDIF, and capture suffix.
pub fn parse(ctx: *ParseContext) anyerror!?ParseResult {
    const script = ctx.locking_script;

    // Walk the script looking for the OP_FALSE OP_IF "ord" sequence.
    // Go code: checks (*scr)[startI-2] == 0 && (*scr)[startI-1] == OpIF
    // and op.Data == "ord" with op.Op == OpDATA3 (push of exactly 3 bytes)
    var pos: usize = 0;
    var found_prefix_end: usize = 0;
    var found_pos: usize = 0;
    var found = false;

    while (pos < script.len) {
        const start = pos;
        var iter = ScriptIterator.initBytes(script[pos..]);
        const chunk = try iter.next() orelse break;
        const advance = iter.pos();
        pos += advance;

        if (chunk == .push_data and chunk.push_data.data.len == 3 and
            std.mem.eql(u8, chunk.push_data.data, "ord"))
        {
            // Check that the two bytes before this push are OP_0 OP_IF
            if (start >= 2 and
                script[start - 2] == @intFromEnum(Opcode.OP_0) and
                script[start - 1] == @intFromEnum(Opcode.OP_IF))
            {
                found_prefix_end = start - 2;
                found_pos = pos;
                found = true;
                break;
            }
        }
    }

    if (!found) return null;

    var insc = Inscription{
        .content_type = &.{},
        .content = &.{},
        .parent = null,
        .script_prefix = script[0..found_prefix_end],
        .script_suffix = &.{},
    };

    // Read field-value pairs from found_pos
    var fpos = found_pos;
    while (fpos < script.len) {
        // Read field indicator
        var field_iter = ScriptIterator.initBytes(script[fpos..]);
        const field_chunk = try field_iter.next() orelse break;
        fpos += field_iter.pos();

        // Read value
        var val_iter = ScriptIterator.initBytes(script[fpos..]);
        const val_chunk = try val_iter.next() orelse break;
        fpos += val_iter.pos();

        // Determine field number (matches Go logic)
        // Go: if op.Op > OpPUSHDATA4 && op.Op <= Op16 → field = int(op.Op) - 80
        //     else if len(op.Data) == 1 → field = int(op.Data[0])
        //     else if len(op.Data) > 1 → custom named field
        //     OP_0 (0x00) falls through all branches, field stays 0 (initial value)
        var field: ?usize = null;

        switch (field_chunk) {
            .opcode => |op| {
                if (op == .OP_0) {
                    field = 0;
                } else {
                    const byte = @intFromEnum(op);
                    if (byte > @intFromEnum(Opcode.OP_PUSHDATA4) and byte <= @intFromEnum(Opcode.OP_16)) {
                        field = byte - 80;
                    } else {
                        // Non-push opcode (OP_ENDIF etc) — done with fields
                        break;
                    }
                }
            },
            .push_data => |pd| {
                if (pd.data.len == 1) {
                    field = pd.data[0];
                }
                // len > 1: custom named field — skip for now (Go stores in Fields map)
            },
            .op_return_data => break,
        }

        const f = field orelse continue;

        const val_data = switch (val_chunk) {
            .push_data => |pd| pd.data,
            .opcode => continue,
            .op_return_data => continue,
        };

        switch (f) {
            0 => {
                // Field 0 = content — break after this
                insc.content = val_data;
                break;
            },
            1 => {
                // Field 1 = content type
                if (val_data.len < 256 and std.unicode.utf8ValidateSlice(val_data)) {
                    insc.content_type = val_data;
                }
            },
            3 => {
                // Field 3 = parent outpoint
                if (val_data.len == 36) {
                    insc.parent = .{
                        .txid = .{ .bytes = val_data[0..32].* },
                        .index = std.mem.readInt(u32, val_data[32..36], .little),
                    };
                } else if (val_data.len == 32) {
                    insc.parent = .{
                        .txid = .{ .bytes = val_data[0..32].* },
                        .index = 0,
                    };
                }
            },
            else => {},
        }
    }

    // After content, read OP_ENDIF and capture suffix
    if (fpos < script.len) {
        var end_iter = ScriptIterator.initBytes(script[fpos..]);
        if (try end_iter.next()) |end_chunk| {
            if (end_chunk == .opcode and end_chunk.opcode == .OP_ENDIF) {
                insc.script_suffix = script[fpos + end_iter.pos() ..];
            }
        }
    }

    // Build events
    var result = ParseResult.init("insc");
    const data = try ctx.allocator.create(Inscription);
    data.* = insc;
    result.data = data;

    // type events
    if (insc.content_type.len > 0) {
        // Base type (before ';')
        if (std.mem.indexOf(u8, insc.content_type, ";")) |semi| {
            // Base type (before ';') and also before any parameters
            if (std.mem.indexOf(u8, insc.content_type[0..semi], "/")) |_| {
                const base = insc.content_type[0..semi];
                try result.addEvent(ctx.allocator, base);
            }
        }
        try result.addEvent(ctx.allocator, insc.content_type);
    }

    // parent event
    if (insc.parent) |parent| {
        // Reverse txid for display format
        var reversed: [32]u8 = undefined;
        for (&reversed, 0..) |*b, i| b.* = parent.txid.bytes[31 - i];
        var hex_buf: [64]u8 = undefined;
        for (&reversed, 0..) |b, i| {
            hex_buf[i * 2] = "0123456789abcdef"[b >> 4];
            hex_buf[i * 2 + 1] = "0123456789abcdef"[b & 0x0f];
        }
        var out_buf: [72]u8 = undefined;
        const formatted = std.fmt.bufPrint(&out_buf, "{s}_{d}", .{
            hex_buf[0..64],
            parent.index,
        }) catch unreachable;
        try result.addEvent(ctx.allocator, formatted);
    }

    // Owner from suffix P2PKH
    if (insc.script_suffix.len >= 25) {
        const suffix_script = Script.init(insc.script_suffix[0..25]);
        if (standard.isP2PKH(suffix_script)) {
            if (standard.publicKeyHash(suffix_script)) |pkh| {
                try result.addOwner(ctx.allocator, pkh.bytes);
            } else |_| {}
        }
    }

    return result;
}

test "parse basic inscription" {
    const allocator = std.testing.allocator;

    // Build: OP_0 OP_IF PUSH("ord") OP_1 PUSH("text/plain") OP_0 PUSH("hello") OP_ENDIF
    var script_buf: [128]u8 = undefined;
    var pos: usize = 0;

    script_buf[pos] = @intFromEnum(Opcode.OP_0);
    pos += 1;
    script_buf[pos] = @intFromEnum(Opcode.OP_IF);
    pos += 1;
    script_buf[pos] = 3; // push 3 bytes
    pos += 1;
    @memcpy(script_buf[pos..][0..3], "ord");
    pos += 3;
    // Field 1 (content type): OP_1
    script_buf[pos] = @intFromEnum(Opcode.OP_1);
    pos += 1;
    // Value: "text/plain"
    script_buf[pos] = 10;
    pos += 1;
    @memcpy(script_buf[pos..][0..10], "text/plain");
    pos += 10;
    // Field 0 (content): OP_0
    script_buf[pos] = @intFromEnum(Opcode.OP_0);
    pos += 1;
    // Value: "hello"
    script_buf[pos] = 5;
    pos += 1;
    @memcpy(script_buf[pos..][0..5], "hello");
    pos += 5;
    // OP_ENDIF
    script_buf[pos] = @intFromEnum(Opcode.OP_ENDIF);
    pos += 1;

    var ctx = ParseContext.init(allocator, script_buf[0..pos], 1, null);
    defer ctx.deinit();

    var result = (try parse(&ctx)).?;
    defer {
        if (result.data) |d| allocator.destroy(@as(*Inscription, @ptrCast(@alignCast(d))));
        result.deinit(allocator);
    }
    try std.testing.expectEqualStrings("insc", result.tag);

    const insc: *Inscription = @ptrCast(@alignCast(result.data.?));
    try std.testing.expectEqualStrings("text/plain", insc.content_type);
    try std.testing.expectEqualStrings("hello", insc.content);
    try std.testing.expectEqual(@as(usize, 0), insc.script_prefix.len);
    try std.testing.expect(insc.parent == null);
}

test "parse inscription returns null for non-inscription" {
    const allocator = std.testing.allocator;
    var script = [_]u8{ 0x76, 0xa9, 20 } ++ [_]u8{0xaa} ** 20 ++ [_]u8{ 0x88, 0xac };

    var ctx = ParseContext.init(allocator, &script, 1, null);
    defer ctx.deinit();

    try std.testing.expect(try parse(&ctx) == null);
}
