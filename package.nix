{
  lib,
  buildGoModule,
  tailwindcss_4,
  fetchFromGitHub,
  source ? fetchFromGitHub {
    owner = "teal-fm";
    repo = "piper";
    # rev = "9920d5900c7cc317170bed1c54ea323879eba83c";
    # hash = "sha256-k00Wkt7uhSZoxAz76IGEYpRDJLKhXT4eSGtdHvFp8jU=";
  },
}:
buildGoModule {
  pname = "tealfm-piper";
  version = "0.0.4";

  src = source;

  vendorHash = "sha256-0CAKzBBARoHSqDv34Xx3Yek6r33Exhrhvn+FzGlby14=";

  nativeBuildInputs = [ tailwindcss_4 ];

  env.CGO_ENABLED = 1;

  subPackages = [ "cmd" ];

  ldflags = [
    "-s"
    "-w"
  ];

  postBuild = ''
    cp -r ./pages/templates $out/
    cp -r ./pages/static $out/
    tailwindcss -i $out/static/base.css -o $out/static/main.css -m
  '';

  postInstall = ''
    mv $out/bin/cmd $out/bin/piper
  '';

  meta = with lib; {
    description = "Music scrobbler service for teal.fm";
    homepage = "https://github.com/teal-fm/piper";
    license = licenses.mit;
    maintainers = with maintainers; [ ptdewey ];
    mainProgram = "piper";
  };
}
