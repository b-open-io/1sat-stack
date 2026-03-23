const std = @import("std");

pub fn build(b: *std.Build) void {
    const target = b.standardTargetOptions(.{});
    const optimize = b.standardOptimizeOption(.{});

    const bsvz_dep = b.dependency("bsvz", .{});
    const bsvz_mod = bsvz_dep.module("bsvz");

    // WASM module: parse_tx
    const wasm_target = b.resolveTargetQuery(.{
        .cpu_arch = .wasm32,
        .os_tag = .wasi,
    });

    const parse_tx_mod = b.createModule(.{
        .root_source_file = b.path("src/parse/main.zig"),
        .target = wasm_target,
        .optimize = .ReleaseSmall,
    });
    parse_tx_mod.addImport("bsvz", bsvz_mod);

    const parse_tx = b.addExecutable(.{
        .name = "parse_tx",
        .root_module = parse_tx_mod,
    });
    b.installArtifact(parse_tx);

    // Native tests
    const test_step = b.step("test", "Run unit tests");

    const test_mod = b.createModule(.{
        .root_source_file = b.path("src/parse/main.zig"),
        .target = target,
        .optimize = optimize,
    });
    test_mod.addImport("bsvz", bsvz_mod);

    const parse_tests = b.addTest(.{
        .root_module = test_mod,
    });
    const run_parse_tests = b.addRunArtifact(parse_tests);
    test_step.dependOn(&run_parse_tests.step);
}
