{
  description = "vimail — terminal email client with Vim-style modal keybindings";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs = { self, nixpkgs }:
    let
      systems = [ "x86_64-linux" "aarch64-linux" "x86_64-darwin" "aarch64-darwin" ];
      forAllSystems = f: nixpkgs.lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});
      version = "0.17.0";
    in
    {
      packages = forAllSystems (pkgs: rec {
        vimail = pkgs.buildGoModule {
          pname = "vimail";
          inherit version;
          src = self;

          # Build exactly what `go install github.com/gausejakub/vimail@vX` builds:
          # the published pkg/vimtea module from go.mod, not the local go.work checkout.
          env.GOWORK = "off";
          env.CGO_ENABLED = "0";
          vendorHash = "sha256-BIqfoarF5PbORRx4FLx8RuKlR9zZ8YHx6Vwtf+vlQ5U=";

          subPackages = [ "." ];
          ldflags = [ "-s" "-w" ];

          meta = {
            description = "Terminal email client with Vim-style modal keybindings";
            homepage = "https://github.com/gausejakub/vimail";
            license = pkgs.lib.licenses.mit;
            mainProgram = "vimail";
          };
        };
        default = vimail;
      });

      apps = forAllSystems (pkgs: rec {
        vimail = {
          type = "app";
          program = "${self.packages.${pkgs.stdenv.hostPlatform.system}.vimail}/bin/vimail";
        };
        default = vimail;
      });

      overlays.default = final: _prev: {
        vimail = self.packages.${final.stdenv.hostPlatform.system}.vimail;
      };

      devShells = forAllSystems (pkgs: {
        default = pkgs.mkShell {
          packages = [ pkgs.go pkgs.gopls pkgs.gotools ];
        };
      });
    };
}
