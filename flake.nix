{
  description = "audiobook-reader: GOTH web reader with word-synced audiobook text";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs = { self, nixpkgs }:
    let
      systems = [ "x86_64-linux" "aarch64-linux" ];
      forAll = f: nixpkgs.lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});
    in
    {
      devShells = forAll (pkgs: {
        default = pkgs.mkShell {
          packages = [
            pkgs.go
            pkgs.gopls
            pkgs.tailwindcss_4 # standalone CLI, no npm
            pkgs.gnumake
          ];
          # Pure Go build; no C toolchain needed.
          CGO_ENABLED = "0";
        };
      });

      packages = forAll (pkgs: {
        default = pkgs.buildGoModule {
          pname = "audiobook-reader";
          version = "0.1.0";
          src = self;
          # Run `nix build` once; replace with the hash Nix reports.
          vendorHash = pkgs.lib.fakeHash;
          nativeBuildInputs = [ pkgs.tailwindcss_4 ];
          env.CGO_ENABLED = "0";
          # Templ output is committed; only CSS must be built before embed.
          preBuild = ''
            tailwindcss -i internal/web/static/input.css -o internal/web/static/app.css --minify
          '';
          subPackages = [ "cmd/reader" ];
        };
      });
    };
}
